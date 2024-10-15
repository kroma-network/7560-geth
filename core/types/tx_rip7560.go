// Copyright 2021 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package types

import (
	"bytes"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"math/big"
)

// Rip7560AccountAbstractionTx represents an RIP-7560 transaction.
type Rip7560AccountAbstractionTx struct {
	// overlapping fields
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int // a.k.a. maxPriorityFeePerGas
	GasFeeCap  *big.Int // a.k.a. maxFeePerGas
	Gas        uint64
	AccessList AccessList

	// extra fields
	Sender                      *common.Address
	AuthorizationData           []byte
	ExecutionData               []byte
	Paymaster                   *common.Address `rlp:"nil"`
	PaymasterData               []byte
	Deployer                    *common.Address `rlp:"nil"`
	DeployerData                []byte
	BuilderFee                  *big.Int
	ValidationGasLimit          uint64
	PaymasterValidationGasLimit uint64
	PostOpGas                   uint64

	// RIP-7712 two-dimensional nonce (optional), 192 bits
	NonceKey *big.Int
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *Rip7560AccountAbstractionTx) copy() TxData {
	cpy := &Rip7560AccountAbstractionTx{
		//To:            copyAddressPtr(tx.To),
		ExecutionData: common.CopyBytes(tx.ExecutionData),
		Nonce:         tx.Nonce,
		NonceKey:      new(big.Int),
		Gas:           tx.Gas,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		//Value:      new(big.Int),
		ChainID:   new(big.Int),
		GasTipCap: new(big.Int),
		GasFeeCap: new(big.Int),

		Sender:                      copyAddressPtr(tx.Sender),
		AuthorizationData:           common.CopyBytes(tx.AuthorizationData),
		Paymaster:                   copyAddressPtr(tx.Paymaster),
		PaymasterData:               common.CopyBytes(tx.PaymasterData),
		Deployer:                    copyAddressPtr(tx.Deployer),
		DeployerData:                common.CopyBytes(tx.DeployerData),
		BuilderFee:                  new(big.Int),
		ValidationGasLimit:          tx.ValidationGasLimit,
		PaymasterValidationGasLimit: tx.PaymasterValidationGasLimit,
		PostOpGas:                   tx.PostOpGas,
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.BuilderFee != nil {
		cpy.BuilderFee.Set(tx.BuilderFee)
	}
	if tx.NonceKey != nil {
		cpy.NonceKey.Set(tx.NonceKey)
	}
	return cpy
}

// accessors for innerTx.
func (tx *Rip7560AccountAbstractionTx) txType() byte           { return Rip7560Type }
func (tx *Rip7560AccountAbstractionTx) chainID() *big.Int      { return tx.ChainID }
func (tx *Rip7560AccountAbstractionTx) accessList() AccessList { return tx.AccessList }
func (tx *Rip7560AccountAbstractionTx) data() []byte           { return make([]byte, 0) }
func (tx *Rip7560AccountAbstractionTx) gas() uint64            { return tx.Gas }
func (tx *Rip7560AccountAbstractionTx) gasFeeCap() *big.Int    { return tx.GasFeeCap }
func (tx *Rip7560AccountAbstractionTx) gasTipCap() *big.Int    { return tx.GasTipCap }
func (tx *Rip7560AccountAbstractionTx) gasPrice() *big.Int     { return tx.GasFeeCap }
func (tx *Rip7560AccountAbstractionTx) value() *big.Int        { return big.NewInt(0) }
func (tx *Rip7560AccountAbstractionTx) nonce() uint64          { return tx.Nonce }
func (tx *Rip7560AccountAbstractionTx) to() *common.Address    { return nil }

func (tx *Rip7560AccountAbstractionTx) GasPayer() *common.Address {
	if tx.Paymaster != nil && tx.Paymaster.Cmp(common.Address{}) != 0 {
		return tx.Paymaster
	}
	return tx.Sender
}

func SumGas(vals ...uint64) (uint64, error) {
	var sum uint64
	for _, val := range vals {
		if val > 1<<62 {
			return 0, fmt.Errorf("invalid gas values")
		}
		sum += val
	}
	return sum, nil
}

func callDataCost(data []byte) uint64 {
	z := uint64(0)
	for i := 0; i < len(data); i++ {
		if data[i] == 0 {
			z++
		}
	}
	nz := uint64(len(data)) - z
	return nz*params.TxDataNonZeroGasEIP2028 + z*params.TxDataZeroGas
}

func (tx *Rip7560AccountAbstractionTx) PreTransactionGasCost() (uint64, error) {
	calldataGasCost, err := tx.callDataGasCost()
	if err != nil {
		return 0, err
	}
	accessListGasCost := tx.accessListGasCost()
	eip7702CodeInsertionsGasCost := tx.eip7702CodeInsertionsGasCost()
	return params.Rip7560TxGas + calldataGasCost + accessListGasCost + eip7702CodeInsertionsGasCost, nil
}

func (tx *Rip7560AccountAbstractionTx) callDataGasCost() (uint64, error) {
	return SumGas(
		callDataCost(tx.AuthorizationData),
		callDataCost(tx.DeployerData),
		callDataCost(tx.ExecutionData),
		callDataCost(tx.PaymasterData),
	)
}

// note: copied from state_transition.go 'IntrinsicGas' function
func (tx *Rip7560AccountAbstractionTx) accessListGasCost() uint64 {
	if tx.AccessList == nil {
		return 0
	}
	gas := uint64(len(tx.AccessList)) * params.TxAccessListAddressGas
	gas += uint64(tx.AccessList.StorageKeys()) * params.TxAccessListStorageKeyGas
	return gas
}

// note: this function must be implemented if EIP-7702 transactions are enabled
func (tx *Rip7560AccountAbstractionTx) eip7702CodeInsertionsGasCost() uint64 {
	return 0
}

func (tx *Rip7560AccountAbstractionTx) TotalGasLimit() (uint64, error) {
	return SumGas(
		params.Rip7560TxGas,
		tx.Gas, tx.ValidationGasLimit, tx.PaymasterValidationGasLimit, tx.PostOpGas,
	)
}

// IsRip7712Nonce returns true if the transaction uses an RIP-7712 two-dimensional nonce
func (tx *Rip7560AccountAbstractionTx) IsRip7712Nonce() bool {
	return tx.NonceKey != nil && tx.NonceKey.Cmp(big.NewInt(0)) == 1
}

func (tx *Rip7560AccountAbstractionTx) EffectiveGasPrice(baseFee *big.Int) *big.Int {
	return tx.effectiveGasPrice(new(big.Int), baseFee)
}

func (tx *Rip7560AccountAbstractionTx) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

func (tx *Rip7560AccountAbstractionTx) rawSignatureValues() (v, r, s *big.Int) {
	return new(big.Int), new(big.Int), new(big.Int)
}

func (tx *Rip7560AccountAbstractionTx) setSignatureValues(chainID, v, r, s *big.Int) {
	//tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}

// encode the subtype byte and the payload-bearing bytes of the RIP-7560 transaction
func (tx *Rip7560AccountAbstractionTx) encode(b *bytes.Buffer) error {
	zeroAddress := common.Address{}
	txCopy := tx.copy().(*Rip7560AccountAbstractionTx)
	if txCopy.Paymaster != nil && zeroAddress.Cmp(*txCopy.Paymaster) == 0 {
		txCopy.Paymaster = nil
	}
	if txCopy.Deployer != nil && zeroAddress.Cmp(*txCopy.Deployer) == 0 {
		txCopy.Deployer = nil
	}
	return rlp.Encode(b, txCopy)
}

// decode the payload-bearing bytes of the encoded RIP-7560 transaction payload
func (tx *Rip7560AccountAbstractionTx) decode(input []byte) error {
	return rlp.DecodeBytes(input, tx)
}

// Rip7560Transaction an equivalent of a solidity struct only used to encode the 'transaction' parameter
type Rip7560Transaction struct {
	Sender                      common.Address
	NonceKey                    *big.Int
	Nonce                       *big.Int
	ValidationGasLimit          *big.Int
	PaymasterValidationGasLimit *big.Int
	PostOpGasLimit              *big.Int
	CallGasLimit                *big.Int
	MaxFeePerGas                *big.Int
	MaxPriorityFeePerGas        *big.Int
	BuilderFee                  *big.Int
	Paymaster                   common.Address
	PaymasterData               []byte
	Deployer                    common.Address
	DeployerData                []byte
	ExecutionData               []byte
	AuthorizationData           []byte
}

func (tx *Rip7560AccountAbstractionTx) AbiEncode() ([]byte, error) {
	structThing, _ := abi.NewType("tuple", "struct thing", []abi.ArgumentMarshaling{
		{Name: "sender", Type: "address"},
		{Name: "nonceKey", Type: "uint256"},
		{Name: "nonce", Type: "uint256"},
		{Name: "validationGasLimit", Type: "uint256"},
		{Name: "paymasterValidationGasLimit", Type: "uint256"},
		{Name: "postOpGasLimit", Type: "uint256"},
		{Name: "callGasLimit", Type: "uint256"},
		{Name: "maxFeePerGas", Type: "uint256"},
		{Name: "maxPriorityFeePerGas", Type: "uint256"},
		{Name: "builderFee", Type: "uint256"},
		{Name: "paymaster", Type: "address"},
		{Name: "paymasterData", Type: "bytes"},
		{Name: "deployer", Type: "address"},
		{Name: "deployerData", Type: "bytes"},
		{Name: "executionData", Type: "bytes"},
		{Name: "authorizationData", Type: "bytes"},
	})

	args := abi.Arguments{
		{Type: structThing, Name: "param_one"},
	}

	paymaster := tx.Paymaster
	if paymaster == nil {
		paymaster = &common.Address{}
	}
	deployer := tx.Deployer
	if deployer == nil {
		deployer = &common.Address{}
	}

	record := &Rip7560Transaction{
		Sender:                      *tx.Sender,
		NonceKey:                    tx.NonceKey,
		Nonce:                       big.NewInt(int64(tx.Nonce)),
		ValidationGasLimit:          big.NewInt(int64(tx.ValidationGasLimit)),
		PaymasterValidationGasLimit: big.NewInt(int64(tx.PaymasterValidationGasLimit)),
		PostOpGasLimit:              big.NewInt(int64(tx.PostOpGas)),
		CallGasLimit:                big.NewInt(int64(tx.Gas)),
		MaxFeePerGas:                tx.GasFeeCap,
		MaxPriorityFeePerGas:        tx.GasTipCap,
		BuilderFee:                  tx.BuilderFee,
		Paymaster:                   *paymaster,
		PaymasterData:               tx.PaymasterData,
		Deployer:                    *deployer,
		DeployerData:                tx.DeployerData,
		ExecutionData:               tx.ExecutionData,
		AuthorizationData:           tx.AuthorizationData,
	}
	packed, err := args.Pack(&record)
	return packed, err
}

// ExternallyReceivedBundle represents a bundle of Type 4 transactions received from a trusted 3rd party.
// The validator includes the bundle in the original order atomically or drops it completely.
type ExternallyReceivedBundle struct {
	BundlerId     string
	BundleHash    common.Hash
	ValidForBlock *big.Int
	Transactions  []*Transaction
}

// BundleReceipt represents a receipt for an ExternallyReceivedBundle successfully included in a block.
type BundleReceipt struct {
	BundleHash          common.Hash
	Count               uint64
	Status              uint64 // 0=included / 1=pending / 2=invalid / 3=unknown
	BlockNumber         uint64
	BlockHash           common.Hash
	TransactionReceipts []*Receipt
	GasUsed             uint64
	GasPaidPriority     *big.Int
	BlockTimestamp      uint64
}

type Rip7560TransactionDebugInfo struct {
	TxHash           common.Hash
	RevertEntityName string
	FrameReverted    bool // true if reverted, false if did not call EntryPoint callback
	RevertData       string
}
