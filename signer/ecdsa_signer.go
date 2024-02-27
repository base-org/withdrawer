package signer

import (
	"crypto/ecdsa"
	"math/big"

	opcrypto "github.com/ethereum-optimism/optimism/op-service/crypto"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ecdsaSigner represents an ECDSA signer.
type ecdsaSigner struct {
	*ecdsa.PrivateKey
}

// Address returns the address associated with the ECDSA signer.
func (s *ecdsaSigner) Address() common.Address {
	return crypto.PubkeyToAddress(s.PublicKey)
}

// SignerFn returns a signer function using the ECDSA private key and chain ID.
func (s *ecdsaSigner) SignerFn(chainID *big.Int) bind.SignerFn {
	return opcrypto.PrivateKeySignerFn(s.PrivateKey, chainID)
}

// SignData signs the given data using the ECDSA private key.
func (s *ecdsaSigner) SignData(data []byte) ([]byte, error) {
	sig, err := crypto.Sign(crypto.Keccak256(data), s.PrivateKey)
	if err != nil {
		return nil, err
	}
	// Adjust the recovery ID for Ethereum compatibility
	sig[crypto.RecoveryIDOffset] += 27
	return sig, nil
}

