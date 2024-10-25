package gasestimator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

func executeRip7560Validation(ctx context.Context, tx *types.Transaction, opts *Options, gasLimit uint64) (*core.ValidationPhaseResult, *state.StateDB, error) {
	st := tx.Rip7560TransactionData()
	// Configure the call for this specific execution (and revert the change after)
	defer func(gas uint64) { st.ValidationGasLimit = gas }(st.ValidationGasLimit)
	st.ValidationGasLimit = gasLimit

	// Execute the call and separate execution faults caused by a lack of gas or
	// other non-fixable conditions
	var (
		blockContext = core.NewEVMBlockContext(opts.Header, opts.Chain, nil, opts.Config, opts.State)
		txContext    = vm.TxContext{
			Origin:   *tx.Rip7560TransactionData().Sender,
			GasPrice: tx.GasFeeCap(),
		}

		dirtyState = opts.State.Copy()
		evm        = vm.NewEVM(blockContext, txContext, dirtyState, opts.Config, vm.Config{NoBaseFee: true})
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		evm.Cancel()
	}()

	// Gas Pool is set to half of the maximum possible gas to prevent overflow
	vpr, err := core.ApplyRip7560ValidationPhases(opts.Config, opts.Chain, &opts.Header.Coinbase, new(core.GasPool).AddGas(math.MaxUint64/2), dirtyState, opts.Header, tx, evm.Config, true)
	if err != nil {
		if errors.Is(err, vm.ErrOutOfGas) {
			return nil, nil, nil // Special case, raise gas limit
		}
		return nil, nil, err // Bail out
	}
	return vpr, dirtyState, nil
}

func EstimateRip7560Validation(ctx context.Context, tx *types.Transaction, opts *Options, gasCap uint64) (uint64, error) {
	// Binary search the gas limit, as it may need to be higher than the amount used
	st := tx.Rip7560TransactionData()
	totalGasLimit, err := st.TotalGasLimit()
	if err != nil {
		return 0, err
	}
	gasLimit := totalGasLimit - st.PostOpGas
	var (
		lo uint64 // lowest-known gas limit where tx execution fails
		hi uint64 // lowest-known gas limit where tx execution succeeds
	)
	// Determine the highest gas limit can be used during the estimation.
	hi = opts.Header.GasLimit
	if gasLimit >= params.TxGas {
		hi = gasLimit
	}
	// Normalize the max fee per gas the call is willing to spend.
	var feeCap *big.Int
	if st.GasFeeCap != nil {
		feeCap = st.GasFeeCap
	} else {
		feeCap = common.Big0
	}
	// Recap the highest gas limit with account's available balance.
	if feeCap.BitLen() != 0 {
		var payment common.Address
		if st.Paymaster == nil {
			payment = *st.Sender
		} else {
			payment = *st.Paymaster
		}
		balance := opts.State.GetBalance(payment).ToBig()

		allowance := new(big.Int).Div(balance, feeCap)

		// If the allowance is larger than maximum uint64, skip checking
		if allowance.IsUint64() && hi > allowance.Uint64() {
			log.Debug("Gas estimation capped by limited funds", "original", hi, "balance", balance,
				"maxFeePerGas", feeCap, "fundable", allowance)
			hi = allowance.Uint64()
		}
	}
	// Recap the highest gas allowance with specified gascap.
	if gasCap != 0 && hi > gasCap {
		log.Debug("Caller gas above allowance, capping", "requested", hi, "cap", gasCap)
		hi = gasCap
	}

	// We first execute the transaction at the highest allowable gas limit, since if this fails we
	// can return error immediately.
	vpr, statedb, err := executeRip7560Validation(ctx, tx, opts, hi)
	if err != nil {
		return 0, err
	} else if vpr == nil && err == nil {
		return 0, fmt.Errorf("gas required exceeds allowance (%d)", hi)
	}
	// For almost any transaction, the gas consumed by the unconstrained execution
	// above lower-bounds the gas limit required for it to succeed. One exception
	// is those that explicitly check gas remaining in order to execute within a
	// given limit, but we probably don't want to return the lowest possible gas
	// limit for these cases anyway.
	vpUsedGas, _ := vpr.ValidationPhaseUsedGas()
	lo = vpUsedGas - 1

	// There's a fairly high chance for the transaction to execute successfully
	// with gasLimit set to the first execution's usedGas + gasRefund. Explicitly
	// check that gas amount and use as a limit for the binary search.
	optimisticGasLimit := (vpUsedGas + params.CallStipend) * 64 / 63
	if optimisticGasLimit < hi {
		vpr, statedb, err = executeRip7560Validation(ctx, tx, opts, optimisticGasLimit)
		if err != nil {
			// This should not happen under normal conditions since if we make it this far the
			// transaction had run without error at least once before.
			log.Error("Execution error in estimate gas", "err", err)
			return 0, err
		}
		if vpr == nil {
			lo = optimisticGasLimit
		} else {
			hi = optimisticGasLimit
		}
	}
	// Binary search for the smallest gas limit that allows the tx to execute successfully.
	for lo+1 < hi {
		if opts.ErrorRatio > 0 {
			// It is a bit pointless to return a perfect estimation, as changing
			// network conditions require the caller to bump it up anyway. Since
			// wallets tend to use 20-25% bump, allowing a small approximation
			// error is fine (as long as it's upwards).
			if float64(hi-lo)/float64(hi) < opts.ErrorRatio {
				break
			}
		}
		mid := (hi + lo) / 2
		if mid > lo*2 {
			// Most txs don't need much higher gas limit than their gas used, and most txs don't
			// require near the full block limit of gas, so the selection of where to bisect the
			// range here is skewed to favor the low side.
			mid = lo * 2
		}
		vpr, statedb, err = executeRip7560Validation(ctx, tx, opts, mid)
		if err != nil {
			// This should not happen under normal conditions since if we make it this far the
			// transaction had run without error at least once before.
			log.Error("Execution error in estimate gas", "err", err)
			return 0, err
		}
		if vpr == nil {
			lo = mid
		} else {
			hi = mid
		}
	}

	opts.ValidationPhaseResult = vpr
	opts.State = statedb
	return hi, nil
}

func executeRip7560Execution(ctx context.Context, tx *types.Transaction, opts *Options, gasLimit uint64) (bool, *core.ExecutionResult, *core.ExecutionResult, error) {
	st := tx.Rip7560TransactionData()
	// Configure the call for this specific execution (and revert the change after)
	defer func(gas uint64) { st.Gas = gas }(st.Gas)
	st.Gas = gasLimit

	// Execute the call and separate execution faults caused by a lack of gas or
	// other non-fixable conditions
	var (
		blockContext = core.NewEVMBlockContext(opts.Header, opts.Chain, nil, opts.Config, opts.State)
		txContext    = vm.TxContext{
			Origin:   *tx.Rip7560TransactionData().Sender,
			GasPrice: tx.GasFeeCap(),
		}

		dirtyState = opts.State.Copy()
		evm        = vm.NewEVM(blockContext, txContext, dirtyState, opts.Config, vm.Config{NoBaseFee: true})
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		evm.Cancel()
	}()

	// Gas Pool is set to half of the maximum possible gas to prevent overflow.
	// Unused gas penalty is not taken into account, since it does not affect the estimation.
	_, exr, ppr, err := core.ApplyRip7560ExecutionPhase(opts.Config, opts.ValidationPhaseResult, opts.Chain, &opts.Header.Coinbase, new(core.GasPool).AddGas(math.MaxUint64/2), dirtyState, opts.Header, vm.Config{NoBaseFee: true}, new(uint64))
	//exr, ppr, _, err := core.ApplyRip7560ExecutionPhase(opts.Config, opts.ValidationPhaseResult, opts.Chain, &opts.Header.Coinbase, new(core.GasPool).AddGas(math.MaxUint64/2), dirtyState, opts.Header, vm.Config{NoBaseFee: true})
	if err != nil {
		if errors.Is(err, core.ErrIntrinsicGas) {
			return true, nil, nil, nil // Special case, raise gas limit
		}
		return true, nil, nil, err // Bail out
	}
	return false, exr, ppr, nil
}

func EstimateRip7560Execution(ctx context.Context, opts *Options, gasCap uint64) (uint64, []byte, error) {
	// Binary search the gas limit, as it may need to be higher than the amount used
	tx := opts.ValidationPhaseResult.Tx
	st := tx.Rip7560TransactionData()
	gasLimit := st.Gas + st.PostOpGas
	var (
		lo uint64 // lowest-known gas limit where tx execution fails
		hi uint64 // lowest-known gas limit where tx execution succeeds
	)
	// Determine the highest gas limit can be used during the estimation.
	hi = opts.Header.GasLimit
	if gasLimit >= params.TxGas {
		hi = gasLimit
	}
	// Normalize the max fee per gas the call is willing to spend.
	var feeCap *big.Int
	if st.GasFeeCap != nil {
		feeCap = st.GasFeeCap
	} else {
		feeCap = common.Big0
	}
	// Recap the highest gas limit with account's available balance.
	if feeCap.BitLen() != 0 {
		var payment common.Address
		if st.Paymaster == nil {
			payment = *st.Sender
		} else {
			payment = *st.Paymaster
		}
		balance := opts.State.GetBalance(payment).ToBig()

		allowance := new(big.Int).Div(balance, feeCap)

		// If the allowance is larger than maximum uint64, skip checking
		if allowance.IsUint64() && hi > allowance.Uint64() {
			log.Debug("Gas estimation capped by limited funds", "original", hi, "balance", balance,
				"maxFeePerGas", feeCap, "fundable", allowance)
			hi = allowance.Uint64()
		}
	}
	// Recap the highest gas allowance with specified gascap.
	if gasCap != 0 && hi > gasCap {
		log.Debug("Caller gas above allowance, capping", "requested", hi, "cap", gasCap)
		hi = gasCap
	}

	// We first execute the transaction at the highest allowable gas limit, since if this fails we
	// can return error immediately.
	failed, exr, ppr, err := executeRip7560Execution(ctx, tx, opts, hi)
	if err != nil {
		return 0, nil, err
	}
	if failed {
		if exr != nil && ppr != nil {
			if !errors.Is(exr.Err, vm.ErrOutOfGas) {
				return 0, exr.Revert(), exr.Err
			} else if !errors.Is(ppr.Err, vm.ErrOutOfGas) {
				return 0, ppr.Revert(), ppr.Err
			}
		}
		return 0, nil, fmt.Errorf("gas required exceeds allowance (%d)", hi)
	}
	// For almost any transaction, the gas consumed by the unconstrained execution
	// above lower-bounds the gas limit required for it to succeed. One exception
	// is those that explicitly check gas remaining in order to execute within a
	// given limit, but we probably don't want to return the lowest possible gas
	// limit for these cases anyway.
	if ppr == nil {
		lo = exr.UsedGas - 1
	} else {
		lo = exr.UsedGas + ppr.UsedGas - 1
	}

	// There's a fairly high chance for the transaction to execute successfully
	// with gasLimit set to the first execution's usedGas + gasRefund. Explicitly
	// check that gas amount and use as a limit for the binary search.
	var optimisticGasLimit uint64
	if ppr == nil {
		optimisticGasLimit = (exr.UsedGas + exr.RefundedGas + params.CallStipend) * 64 / 63
	} else {
		optimisticGasLimit = (exr.UsedGas + exr.RefundedGas + ppr.UsedGas + ppr.RefundedGas + params.CallStipend) * 64 / 63
	}
	if optimisticGasLimit < hi {
		failed, _, _, err = executeRip7560Execution(ctx, tx, opts, optimisticGasLimit)
		if err != nil {
			// This should not happen under normal conditions since if we make it this far the
			// transaction had run without error at least once before.
			log.Error("Execution error in estimate gas", "err", err)
			return 0, nil, err
		}
		if failed {
			lo = optimisticGasLimit
		} else {
			hi = optimisticGasLimit
		}
	}
	// Binary search for the smallest gas limit that allows the tx to execute successfully.
	for lo+1 < hi {
		if opts.ErrorRatio > 0 {
			// It is a bit pointless to return a perfect estimation, as changing
			// network conditions require the caller to bump it up anyway. Since
			// wallets tend to use 20-25% bump, allowing a small approximation
			// error is fine (as long as it's upwards).
			if float64(hi-lo)/float64(hi) < opts.ErrorRatio {
				break
			}
		}
		mid := (hi + lo) / 2
		if mid > lo*2 {
			// Most txs don't need much higher gas limit than their gas used, and most txs don't
			// require near the full block limit of gas, so the selection of where to bisect the
			// range here is skewed to favor the low side.
			mid = lo * 2
		}
		failed, _, _, err = executeRip7560Execution(ctx, tx, opts, mid)
		if err != nil {
			// This should not happen under normal conditions since if we make it this far the
			// transaction had run without error at least once before.
			log.Error("Execution error in estimate gas", "err", err)
			return 0, nil, err
		}
		if failed {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi, nil, nil
}
