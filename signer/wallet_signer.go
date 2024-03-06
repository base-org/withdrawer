package signer

import (
	"crypto/ecdsa"
	"math/big"

	"github.com/decred/dcrd/hdkeychain/v3"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
)

// walletSigner represents a signer based on a wallet account.
type walletSigner struct {
	wallet  accounts.Wallet
	account accounts.Account
}

// Address returns the Ethereum address associated with the signer.
func (s *walletSigner) Address() common.Address {
	return s.account.Address
}

// SignerFn returns a signer function used for transaction signing.
func (s *walletSigner) SignerFn(chainID *big.Int) bind.SignerFn {
	return func(address common.Address, tx *types.Transaction) (*types.Transaction, error) {
		return s.wallet.SignTx(s.account, tx, chainID)
	}
}

// SignData signs the given data using the signer's private key.
func (s *walletSigner) SignData(data []byte) ([]byte, error) {
	return s.wallet.SignData(s.account, accounts.MimetypeTypedData, data)
}

// derivePrivateKeyFromMnemonic derives an ECDSA private key from a mnemonic phrase and derivation path.
func derivePrivateKeyFromMnemonic(mnemonic string, path accounts.DerivationPath) (*ecdsa.PrivateKey, error) {
	// Parse the seed string into the master BIP32 key.
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return nil, err
	}

	// Derive the private key based on the derivation path.
	privKey, err := hdkeychain.NewMaster(seed, fakeNetworkParams{})
	if err != nil {
		return nil, err
	}

	for _, child := range path {
		privKey, err = privKey.Child(child)
		if err != nil {
			return nil, err
		}
	}

	// Serialize the private key and convert it to an ECDSA private key.
	rawPrivKey, err := privKey.SerializedPrivKey()
	if err != nil {
		return nil, err
	}

	return crypto.ToECDSA(rawPrivKey)
}

// fakeNetworkParams is used for HD key derivation and provides fake network parameters.
type fakeNetworkParams struct{}

func (f fakeNetworkParams) HDPrivKeyVersion() [4]byte {
	return [4]byte{}
}

func (f fakeNetworkParams) HDPubKeyVersion() [4]byte {
	return [4]byte{}
}
