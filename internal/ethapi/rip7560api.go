package ethapi

import (
	"context"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"golang.org/x/crypto/sha3"
	"math/big"
)

func (s *TransactionAPI) SendRip7560TransactionsBundle(ctx context.Context, args []TransactionArgs, creationBlock *big.Int, bundlerId string) (common.Hash, error) {
	if len(args) == 0 {
		return common.Hash{}, errors.New("submitted bundle has zero length")
	}
	txs := make([]*types.Transaction, len(args))
	for i := 0; i < len(args); i++ {
		txs[i] = args[i].ToTransaction()
	}
	bundle := &types.ExternallyReceivedBundle{
		BundlerId:     bundlerId,
		ValidForBlock: creationBlock,
		Transactions:  txs,
	}
	bundleHash := CalculateBundleHash(txs)
	bundle.BundleHash = bundleHash
	err := SubmitRip7560Bundle(ctx, s.b, bundle)
	if err != nil {
		return common.Hash{}, err
	}
	return bundleHash, nil
}

func (s *TransactionAPI) GetRip7560BundleStatus(ctx context.Context, hash common.Hash) (*types.BundleReceipt, error) {
	bundleStats, err := s.b.GetRip7560BundleStatus(ctx, hash)
	return bundleStats, err
}

func (s *TransactionAPI) GetRip7560TransactionDebugInfo(hash common.Hash) (map[string]interface{}, error) {
	return s.b.GetRip7560TransactionDebugInfo(hash)
}

// CalculateBundleHash
// TODO: If this code is indeed necessary, keep it in utils; better - remove altogether.
func CalculateBundleHash(txs []*types.Transaction) common.Hash {
	appendedTxIds := make([]byte, 0)
	for _, tx := range txs {
		txHash := tx.Hash()
		appendedTxIds = append(appendedTxIds, txHash[:]...)
	}

	bundleHash := rlpHash(appendedTxIds)
	println("calculateBundleHash")
	println(bundleHash.String())
	return bundleHash
}

func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

// SubmitRip7560Bundle is a helper function that submits a bundle of Type 4 transactions to txPool and logs a message.
func SubmitRip7560Bundle(ctx context.Context, b Backend, bundle *types.ExternallyReceivedBundle) error {
	return b.SubmitRip7560Bundle(bundle)
}
