package rip7560

import (
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/tests"
	"github.com/stretchr/testify/assert"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestPackValidationData(t *testing.T) {
	//assert.Equal(t, make([]byte, 32), packValidationData(0, 0, 0))
	//assert.Equal(t, new(big.Int).SetInt64(0x1234).Text(16), new(big.Int).SetBytes(packValidationData(0x1234, 0, 0)).Text(16))
	// ------------------------------------ bbbbbbbbbbbb-aaaaaaaaaaa-mmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmm
	packed, _ := new(big.Int).SetString("0000000000020000000000010000000000000000000000000000000000001234", 16)
	assert.Equal(t, packed.Text(16), new(big.Int).SetBytes(core.PackValidationData(0x1234, 1, 2)).Text(16))
}

func TestUnpackValidationData(t *testing.T) {
	packed := core.PackValidationData(0xdead, 0xcafe, 0xface)
	magic, until, after := core.UnpackValidationData(packed)
	assert.Equal(t, []uint64{0xdead, 0xcafe, 0xface}, []uint64{magic, until, after})
}

func TestValidationFailure_OOG(t *testing.T) {
	magic := big.NewInt(0xbf45c166)
	magic.Lsh(magic, 256-32)

	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER, returnData(magic.Bytes()), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1),
		GasFeeCap:     big.NewInt(1000000000),
	}, "out of gas")
}

func TestValidationFailure_no_balance(t *testing.T) {
	magic := big.NewInt(0xbf45c166)
	magic.Lsh(magic, 256-32)

	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER, returnData(magic.Bytes()), 1), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1),
		GasFeeCap:     big.NewInt(1000000000),
	}, "insufficient funds for gas * price + value: address 0x1111111111222222222233333333334444444444 have 1 want 1000000000")
}

func TestValidationFailure_sigerror(t *testing.T) {
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER, returnData(core.PackValidationData(core.MAGIC_VALUE_SIGFAIL, 0, 0)), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "account signature error")
}

func TestValidation_ok(t *testing.T) {

	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER, createAccountCode(), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "ok")
}

func TestValidation_ok_paid(t *testing.T) {

	aatx := types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}
	tb := newTestContextBuilder(t).withCode(DEFAULT_SENDER, createAccountCode(), DEFAULT_BALANCE)
	validatePhase(tb, aatx, "ok")

	maxCost := new(big.Int).SetUint64(aatx.ValidationGas + aatx.PaymasterGas + aatx.Gas)
	maxCost.Mul(maxCost, aatx.GasFeeCap)
}

func TestValidationFailure_account_revert(t *testing.T) {
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER,
		createCode(vm.PUSH0, vm.DUP1, vm.REVERT), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "execution reverted")
}

func TestValidationFailure_account_out_of_range(t *testing.T) {
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER,
		createCode(vm.PUSH0, vm.DUP1, vm.REVERT), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "execution reverted")
}

func TestValidationFailure_account_wrong_return_length(t *testing.T) {
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER,
		returnData([]byte{1, 2, 3}), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "invalid account return data length")
}

func TestValidationFailure_account_no_return_value(t *testing.T) {
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER,
		returnData([]byte{}), DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "invalid account return data length")
}

func TestValidationFailure_account_wrong_return_value(t *testing.T) {
	// create buffer of 32 byte array
	validatePhase(newTestContextBuilder(t).withCode(DEFAULT_SENDER,
		returnData(make([]byte, 32)),
		DEFAULT_BALANCE), types.Rip7560AccountAbstractionTx{
		ValidationGas: uint64(1000000000),
		GasFeeCap:     big.NewInt(1000000000),
	}, "account did not return correct MAGIC_VALUE")
}

func validatePhase(tb *testContextBuilder, aatx types.Rip7560AccountAbstractionTx, expectedErr string) *core.ValidationPhaseResult {
	t := tb.build()
	if aatx.Sender == nil {
		//pre-deployed sender account
		Sender := common.HexToAddress(DEFAULT_SENDER)
		aatx.Sender = &Sender
	}
	tx := types.NewTx(&aatx)

	var state = tests.MakePreState(rawdb.NewMemoryDatabase(), t.genesisAlloc, false, rawdb.HashScheme)
	defer state.Close()

	state.StateDB.SetTxContext(tx.Hash(), 0)
	err := core.BuyGasRip7560Transaction(&aatx, state.StateDB)

	var res *core.ValidationPhaseResult
	if err == nil {
		res, err = core.ApplyRip7560ValidationPhases(t.genesis.Config, t.chainContext, &common.Address{}, t.gaspool, state.StateDB, t.genesisBlock.Header(), tx, vm.Config{})
		// err string or empty if nil
	}
	errStr := "ok"
	if err != nil {
		errStr = err.Error()
	}
	assert.Equal(t.t, expectedErr, errStr)
	return res
}

//test failure on non-rip7560

//IntrinsicGas: for validation frame, should return the max possible gas.
// - execution should be "free" (and refund the excess)
// geth increment nonce before "call" our validation frame. (in ApplyMessage)
