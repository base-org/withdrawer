package signer

import (
	"crypto/ecdsa"
	"math/big"

	opcrypto "github.com/ethereum-optimism/optimism/op-service/crypto"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type ecdsaSigner struct {
	*ecdsa.PrivateKey
}

func (s *ecdsaSigner) Address() common.Address {
	return crypto.PubkeyToAddress(s.PublicKey)
}

func (s *ecdsaSigner) SignerFn(chainID *big.Int) bind.SignerFn {
	return opcrypto.PrivateKeySignerFn(s.PrivateKey, chainID)
}

func (s *ecdsaSigner) SignData(data []byte) ([]byte, error) {
	sig, err := crypto.Sign(crypto.Keccak256(data), s.PrivateKey)
	if err != nil {
		return nil, err
	}
	sig[crypto.RecoveryIDOffset] += 27
	return sig, err
}
