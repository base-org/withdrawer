package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-node/bindings"
	bindingspreview "github.com/ethereum-optimism/optimism/op-node/bindings/preview"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/base-org/withdrawer/signer"
	"github.com/base-org/withdrawer/withdraw"
)

type network struct {
	l2RPC              string
	portalAddress      string
	l2OOAddress        string
	disputeGameFactory string
	faultProofs        bool
}

var networks = map[string]network{
	"base-mainnet": {
		l2RPC:              "https://mainnet.base.org",
		portalAddress:      "0x49048044D57e1C92A77f79988d21Fa8fAF74E97e",
		l2OOAddress:        "0x56315b90c40730925ec5485cf004d835058518A0",
		disputeGameFactory: "0x0000000000000000000000000000000000000000",
		faultProofs:        false,
	},
	"base-sepolia": {
		l2RPC:              "https://sepolia.base.org",
		portalAddress:      "0x49f53e41452C74589E85cA1677426Ba426459e85",
		l2OOAddress:        "0x0000000000000000000000000000000000000000",
		disputeGameFactory: "0xd6E6dBf4F7EA0ac412fD8b65ED297e64BB7a06E1",
		faultProofs:        true,
	},
	"op-mainnet": {
		l2RPC:              "https://mainnet.optimism.io",
		portalAddress:      "0xbEb5Fc579115071764c7423A4f12eDde41f106Ed",
		l2OOAddress:        "0x0000000000000000000000000000000000000000",
		disputeGameFactory: "0xe5965Ab5962eDc7477C8520243A95517CD252fA9",
		faultProofs:        true,
	},
	"op-sepolia": {
		l2RPC:              "https://sepolia.optimism.io",
		portalAddress:      "0x16Fc5058F25648194471939df75CF27A2fdC48BC",
		l2OOAddress:        "0x0000000000000000000000000000000000000000",
		disputeGameFactory: "0x05F9613aDB30026FFd634f38e5C4dFd30a197Fa1",
		faultProofs:        true,
	},
}

func main() {
	var networkKeys []string
	for n := range networks {
		networkKeys = append(networkKeys, n)
	}

	var rpcFlag string
	var networkFlag string
	var l2RpcFlag string
	var faultProofs bool
	var portalAddress string
	var l2OOAddress string
	var dgfAddress string
	var withdrawalFlag string
	var privateKey string
	var ledger bool
	var mnemonic string
	var hdPath string

	flag.StringVar(&rpcFlag, "rpc", "", "Ethereum L1 RPC url")
	flag.StringVar(&networkFlag, "network", "base-mainnet", fmt.Sprintf("op-stack network to withdraw.go from (one of: %s)", strings.Join(networkKeys, ", ")))
	flag.StringVar(&l2RpcFlag, "l2-rpc", "", "Custom network L2 RPC url")
	flag.BoolVar(&faultProofs, "fault-proofs", false, "Use fault proofs")
	flag.StringVar(&portalAddress, "portal-address", "", "Custom network OptimismPortal address")
	flag.StringVar(&l2OOAddress, "l2oo-address", "", "Custom network L2OutputOracle address")
	flag.StringVar(&dgfAddress, "dfg-address", "", "Custom network DisputeGameFactory address")
	flag.StringVar(&withdrawalFlag, "withdrawal", "", "TX hash of the L2 withdrawal transaction")
	flag.StringVar(&privateKey, "private-key", "", "Private key to use for signing transactions")
	flag.BoolVar(&ledger, "ledger", false, "Use ledger device for signing transactions")
	flag.StringVar(&mnemonic, "mnemonic", "", "Mnemonic to use for signing transactions")
	flag.StringVar(&hdPath, "hd-path", "m/44'/60'/0'/0/0", "Hierarchical deterministic derivation path for mnemonic or ledger")
	flag.Parse()

	log.SetDefault(oplog.NewLogger(os.Stderr, oplog.DefaultCLIConfig()))

	n, ok := networks[networkFlag]
	if !ok {
		log.Crit("Unknown network", "network", networkFlag)
	}

	// check for non-compatible networks with given flags
	if faultProofs {
		if n.faultProofs == false {
			log.Crit("Fault proofs are not supported on this network")
		}
	} else {
		if n.faultProofs == true {
			log.Crit("Fault proofs are required on this network, please provide the --fault-proofs flag")
		}
	}

	// check for non-empty flags for non-fault proof networks
	if !faultProofs && (l2RpcFlag != "" || portalAddress != "" || l2OOAddress != "") {
		if l2RpcFlag == "" {
			log.Crit("Missing --l2-rpc flag")
		}
		if portalAddress == "" {
			log.Crit("Missing --portal-address flag")
		}
		if l2OOAddress == "" {
			log.Crit("Missing --l2oo-address flag")
		}
		n = network{
			l2RPC:         l2RpcFlag,
			portalAddress: portalAddress,
			l2OOAddress:   l2OOAddress,
			faultProofs:   faultProofs,
		}
	}

	// check for non-empty flags for fault proof networks
	if faultProofs && (l2RpcFlag != "" || dgfAddress != "" || portalAddress != "") {
		if l2RpcFlag == "" {
			log.Crit("Missing --l2-rpc flag")
		}
		if dgfAddress == "" {
			log.Crit("Missing --dfg-address flag")
		}
		if portalAddress == "" {
			log.Crit("Missing --portal-address flag")
		}
		n = network{
			l2RPC:              l2RpcFlag,
			portalAddress:      portalAddress,
			disputeGameFactory: dgfAddress,
			faultProofs:        faultProofs,
		}
	}

	if rpcFlag == "" {
		log.Crit("Missing --rpc flag")
	}

	if withdrawalFlag == "" {
		log.Crit("Missing --withdrawal flag")
	}
	withdrawal := common.HexToHash(withdrawalFlag)

	options := 0
	if privateKey != "" {
		options++
	}
	if ledger {
		options++
	}
	if mnemonic != "" {
		options++
	}
	if options != 1 {
		log.Crit("One (and only one) of --private-key, --ledger, --mnemonic must be set")
	}

	// instantiate shared variables
	s, err := signer.CreateSigner(privateKey, mnemonic, hdPath)
	if err != nil {
		log.Crit("Error creating signer", "error", err)
	}

	ctx := context.Background()

	l1Client, err := ethclient.DialContext(ctx, rpcFlag)
	if err != nil {
		log.Crit("Error dialing L1 client", "error", err)
	}

	l1ChainID, err := l1Client.ChainID(ctx)
	if err != nil {
		log.Crit("Error querying chain ID", "error", err)
	}

	l1Nonce, err := l1Client.PendingNonceAt(ctx, s.Address())
	if err != nil {
		log.Crit("Error querying nonce", "error", err)
	}

	l1opts := &bind.TransactOpts{
		From:    s.Address(),
		Signer:  s.SignerFn(l1ChainID),
		Context: ctx,
		Nonce:   big.NewInt(int64(l1Nonce)),
	}

	l2Client, err := rpc.DialContext(ctx, n.l2RPC)
	if err != nil {
		log.Crit("Error dialing L2 client", "error", err)
	}

	// handle withdrawals with or without the fault proofs withdrawer
	if faultProofs {
		portal, err := bindingspreview.NewOptimismPortal2(common.HexToAddress(n.portalAddress), l1Client)
		if err != nil {
			log.Crit("Error binding OptimismPortal2 contract", "error", err)
		}

		dgf, err := bindings.NewDisputeGameFactory(common.HexToAddress(n.disputeGameFactory), l1Client)
		if err != nil {
			log.Crit("Error binding DisputeGameFactory contract", "error", err)
		}

		withdrawer := withdraw.FPWithdrawer{
			Ctx:      ctx,
			L1Client: l1Client,
			L2Client: l2Client,
			L2TxHash: withdrawal,
			Portal:   portal,
			Factory:  dgf,
			Opts:     l1opts,
		}

		isFinalized, err := withdrawer.IsProofFinalized()
		if err != nil {
			log.Crit("Error querying withdrawal finalization status", "error", err)
		}
		if isFinalized {
			fmt.Println("Withdrawal already finalized")
			return
		}

		// TODO: Add functionality to generate output root proposal and prove to that proposal
		err = withdrawer.CheckIfProvable()
		if err != nil {
			log.Crit("Withdrawal is not provable", "error", err)
		}

		proof, err := withdrawer.GetProvenWithdrawal()
		if err != nil {
			log.Crit("Error querying withdrawal proof", "error", err)
		}

		if proof.Timestamp == 0 {
			err = withdrawer.ProveWithdrawal()
			if err != nil {
				log.Crit("Error proving withdrawal", "error", err)
			}
			fmt.Println("The withdrawal has been successfully proven, finalization of the withdrawal can be done once the dispute game has finished and the finalization period has elapsed")
			return
		}

		// TODO: Add edge-case handling for FPs if a withdrawal needs to be re-proven due to blacklisted / failed dispute game resolution
		err = withdrawer.FinalizeWithdrawal()
		if err != nil {
			log.Crit("Error completing withdrawal", "error", err)
		}
	} else {
		portal, err := bindings.NewOptimismPortal(common.HexToAddress(n.portalAddress), l1Client)
		if err != nil {
			log.Crit("Error binding OptimismPortal contract", "error", err)
		}

		l2oo, err := bindings.NewL2OutputOracle(common.HexToAddress(n.l2OOAddress), l1Client)
		if err != nil {
			log.Crit("Error binding L2OutputOracle contract", "error", err)
		}

		withdrawer := withdraw.Withdrawer{
			Ctx:      ctx,
			L1Client: l1Client,
			L2Client: l2Client,
			L2TxHash: withdrawal,
			Portal:   portal,
			Oracle:   l2oo,
			Opts:     l1opts,
		}

		isFinalized, err := withdrawer.IsProofFinalized()
		if err != nil {
			log.Crit("Error querying withdrawal finalization status", "error", err)
		}
		if isFinalized {
			fmt.Println("Withdrawal already finalized")
			return
		}

		err = withdrawer.CheckIfProvable()
		if err != nil {
			log.Crit("Withdrawal is not provable", "error", err)
		}

		proof, err := withdrawer.GetProvenWithdrawal()
		if err != nil {
			log.Crit("Error querying withdrawal proof", "error", err)
		}

		if proof.Timestamp.Uint64() == 0 {
			err = withdrawer.ProveWithdrawal()
			if err != nil {
				log.Crit("Error proving withdrawal", "error", err)
			}
			fmt.Println("The withdrawal has been successfully proven, finalization of the withdrawal can be done once the finalization period has elapsed")
			return
		}

		err = withdrawer.FinalizeWithdrawal()
		if err != nil {
			log.Crit("Error completing withdrawal", "error", err)
		}
	}
}
