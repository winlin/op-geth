// Copyright 2014 The go-ethereum Authors
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

package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/superchain"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

//go:generate go run github.com/fjl/gencodec -type Genesis -field-override genesisSpecMarshaling -out gen_genesis.go

var errGenesisNoConfig = errors.New("genesis has no chain configuration")

// Deprecated: use types.Account instead.
type GenesisAccount = types.Account

// Deprecated: use types.GenesisAlloc instead.
type GenesisAlloc = types.GenesisAlloc

// Genesis specifies the header fields, state of a genesis block. It also defines hard
// fork switch-over blocks through the chain configuration.
type Genesis struct {
	Config     *params.ChainConfig `json:"config"`
	Nonce      uint64              `json:"nonce"`
	Timestamp  uint64              `json:"timestamp"`
	ExtraData  []byte              `json:"extraData"`
	GasLimit   uint64              `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int            `json:"difficulty" gencodec:"required"`
	Mixhash    common.Hash         `json:"mixHash"`
	Coinbase   common.Address      `json:"coinbase"`
	Alloc      types.GenesisAlloc  `json:"alloc"      gencodec:"required"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number        uint64      `json:"number"`
	GasUsed       uint64      `json:"gasUsed"`
	ParentHash    common.Hash `json:"parentHash"`
	BaseFee       *big.Int    `json:"baseFeePerGas"` // EIP-1559
	ExcessBlobGas *uint64     `json:"excessBlobGas"` // EIP-4844
	BlobGasUsed   *uint64     `json:"blobGasUsed"`   // EIP-4844

	// StateHash represents the genesis state, to allow instantiation of a chain with missing initial state.
	// Chains with history pruning, or extraordinarily large genesis allocation (e.g. after a regenesis event)
	// may utilize this to get started, and then state-sync the latest state, while still verifying the header chain.
	StateHash *common.Hash `json:"stateHash,omitempty"`
}

func ReadGenesis(db ethdb.Database) (*Genesis, error) {
	var genesis Genesis
	stored := rawdb.ReadCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		return nil, fmt.Errorf("invalid genesis hash in database: %x", stored)
	}
	blob := rawdb.ReadGenesisStateSpec(db, stored)
	if blob == nil {
		return nil, errors.New("genesis state missing from db")
	}
	if len(blob) != 0 {
		if err := genesis.Alloc.UnmarshalJSON(blob); err != nil {
			return nil, fmt.Errorf("could not unmarshal genesis state json: %s", err)
		}
	}
	genesis.Config = rawdb.ReadChainConfig(db, stored)
	if genesis.Config == nil {
		return nil, errors.New("genesis config missing from db")
	}
	genesisBlock := rawdb.ReadBlock(db, stored, 0)
	if genesisBlock == nil {
		return nil, errors.New("genesis block missing from db")
	}
	genesisHeader := genesisBlock.Header()
	genesis.Nonce = genesisHeader.Nonce.Uint64()
	genesis.Timestamp = genesisHeader.Time
	genesis.ExtraData = genesisHeader.Extra
	genesis.GasLimit = genesisHeader.GasLimit
	genesis.Difficulty = genesisHeader.Difficulty
	genesis.Mixhash = genesisHeader.MixDigest
	genesis.Coinbase = genesisHeader.Coinbase
	genesis.BaseFee = genesisHeader.BaseFee
	genesis.ExcessBlobGas = genesisHeader.ExcessBlobGas
	genesis.BlobGasUsed = genesisHeader.BlobGasUsed
	// A nil or empty alloc, with a non-matching state-root in the block header, intents to override the state-root.
	if genesis.Alloc == nil || (len(genesis.Alloc) == 0 && genesisHeader.Root != types.EmptyRootHash) {
		h := genesisHeader.Root // the genesis block is encoded as RLP in the DB and will contain the state-root
		genesis.StateHash = &h
		genesis.Alloc = nil
	}

	return &genesis, nil
}

// hashAlloc returns the following:
// * computed state root according to the genesis specification.
// * storage root of the L2ToL1MessagePasser contract.
// * error if any, when committing the genesis state (if so, state root and storage root will be empty).
func hashAlloc(ga *types.GenesisAlloc, isVerkle, isIsthmus bool) (common.Hash, common.Hash, error) {
	// If a genesis-time verkle trie is requested, create a trie config
	// with the verkle trie enabled so that the tree can be initialized
	// as such.
	var config *triedb.Config
	if isVerkle {
		config = &triedb.Config{
			PathDB:   pathdb.Defaults,
			IsVerkle: true,
		}
	}
	// Create an ephemeral in-memory database for computing hash,
	// all the derived states will be discarded to not pollute disk.
	emptyRoot := types.EmptyRootHash
	if isVerkle {
		emptyRoot = types.EmptyVerkleHash
	}
	db := rawdb.NewMemoryDatabase()
	statedb, err := state.New(emptyRoot, state.NewDatabase(triedb.NewDatabase(db, config), nil))
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	for addr, account := range *ga {
		if account.Balance != nil {
			statedb.AddBalance(addr, uint256.MustFromBig(account.Balance), tracing.BalanceIncreaseGenesisBalance)
		}
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}

	stateRoot, err := statedb.Commit(0, false)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	// get the storage root of the L2ToL1MessagePasser contract
	var storageRootMessagePasser common.Hash
	if isIsthmus {
		storageRootMessagePasser = statedb.GetStorageRoot(params.OptimismL2ToL1MessagePasser)
	}

	return stateRoot, storageRootMessagePasser, nil
}

// flushAlloc is very similar with hash, but the main difference is all the
// generated states will be persisted into the given database. Returns the
// same values as hashAlloc.
func flushAlloc(ga *types.GenesisAlloc, triedb *triedb.Database, isIsthmus bool) (common.Hash, common.Hash, error) {
	emptyRoot := types.EmptyRootHash
	if triedb.IsVerkle() {
		emptyRoot = types.EmptyVerkleHash
	}
	statedb, err := state.New(emptyRoot, state.NewDatabase(triedb, nil))
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	for addr, account := range *ga {
		if account.Balance != nil {
			// This is not actually logged via tracer because OnGenesisBlock
			// already captures the allocations.
			statedb.AddBalance(addr, uint256.MustFromBig(account.Balance), tracing.BalanceIncreaseGenesisBalance)
		}
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}
	stateRoot, err := statedb.Commit(0, false)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	// get the storage root of the L2ToL1MessagePasser contract
	var storageRootMessagePasser common.Hash
	if isIsthmus {
		storageRootMessagePasser = statedb.GetStorageRoot(params.OptimismL2ToL1MessagePasser)
	}
	// Commit newly generated states into disk if it's not empty.
	if stateRoot != types.EmptyRootHash {
		if err := triedb.Commit(stateRoot, true); err != nil {
			return common.Hash{}, common.Hash{}, err
		}
	}
	return stateRoot, storageRootMessagePasser, nil
}

func getGenesisState(db ethdb.Database, blockhash common.Hash) (alloc types.GenesisAlloc, err error) {
	blob := rawdb.ReadGenesisStateSpec(db, blockhash)
	if len(blob) != 0 {
		if err := alloc.UnmarshalJSON(blob); err != nil {
			return nil, err
		}

		return alloc, nil
	}

	// Genesis allocation is missing and there are several possibilities:
	// the node is legacy which doesn't persist the genesis allocation or
	// the persisted allocation is just lost.
	// - supported networks(mainnet, testnets), recover with defined allocations
	// - private network, can't recover
	var genesis *Genesis
	switch blockhash {
	case params.MainnetGenesisHash:
		genesis = DefaultGenesisBlock()
	case params.SepoliaGenesisHash:
		genesis = DefaultSepoliaGenesisBlock()
	case params.HoleskyGenesisHash:
		genesis = DefaultHoleskyGenesisBlock()
	}
	if genesis != nil {
		return genesis.Alloc, nil
	}

	return nil, nil
}

// field type overrides for gencodec
type genesisSpecMarshaling struct {
	Nonce         math.HexOrDecimal64
	Timestamp     math.HexOrDecimal64
	ExtraData     hexutil.Bytes
	GasLimit      math.HexOrDecimal64
	GasUsed       math.HexOrDecimal64
	Number        math.HexOrDecimal64
	Difficulty    *math.HexOrDecimal256
	Alloc         map[common.UnprefixedAddress]types.Account
	BaseFee       *math.HexOrDecimal256
	ExcessBlobGas *math.HexOrDecimal64
	BlobGasUsed   *math.HexOrDecimal64
}

// GenesisMismatchError is raised when trying to overwrite an existing
// genesis block with an incompatible one.
type GenesisMismatchError struct {
	Stored, New common.Hash
}

func (e *GenesisMismatchError) Error() string {
	return fmt.Sprintf("database contains incompatible genesis (have %x, new %x)", e.Stored, e.New)
}

// ChainOverrides contains the changes to chain config.
type ChainOverrides struct {
	OverrideCancun *uint64
	OverrideVerkle *uint64

	// OP-Stack additions
	OverrideOptimismCanyon   *uint64
	OverrideOptimismEcotone  *uint64
	OverrideOptimismFjord    *uint64
	OverrideOptimismGranite  *uint64
	OverrideOptimismHolocene *uint64
	OverrideOptimismInterop  *uint64
	ApplySuperchainUpgrades  bool
}

// apply applies the chain overrides on the supplied chain config.
func (o *ChainOverrides) apply(cfg *params.ChainConfig) (*params.ChainConfig, error) {
	if o == nil || cfg == nil {
		return cfg, nil
	}
	cpy := *cfg

	// OP-Stack: If applying the superchain-registry to a known OP-Stack chain,
	// then override the local chain-config with that from the registry.
	if o.ApplySuperchainUpgrades && cfg.IsOptimism() && cfg.ChainID != nil && cfg.ChainID.IsUint64() {
		getChainConfig := func() (*params.ChainConfig, error) {
			chain, err := superchain.GetChain(cfg.ChainID.Uint64())
			if err != nil {
				return nil, err
			}
			chainConf, err := chain.Config()
			if err != nil {
				return nil, err
			}
			genConf, err := params.LoadOPStackChainConfig(chainConf)
			if err != nil {
				return nil, err
			}
			return genConf, err
		}

		if genConf, err := getChainConfig(); err == nil {
			cpy = *genConf
		} else {
			log.Warn("failed to load chain config from superchain-registry, skipping override", "err", err, "chain_id", cfg.ChainID)
		}
	}

	if o.OverrideCancun != nil {
		cpy.CancunTime = o.OverrideCancun
	}
	if o.OverrideVerkle != nil {
		cpy.VerkleTime = o.OverrideVerkle
	}

	// OP-Stack overrides
	if o.OverrideOptimismCanyon != nil {
		cpy.CanyonTime = o.OverrideOptimismCanyon
		cpy.ShanghaiTime = o.OverrideOptimismCanyon
		if cfg.Optimism != nil && (cfg.Optimism.EIP1559DenominatorCanyon == nil || *cfg.Optimism.EIP1559DenominatorCanyon == 0) {
			eip1559DenominatorCanyon := uint64(250)
			cpy.Optimism.EIP1559DenominatorCanyon = &eip1559DenominatorCanyon
		}
	}
	if o.OverrideOptimismEcotone != nil {
		cpy.EcotoneTime = o.OverrideOptimismEcotone
		cpy.CancunTime = o.OverrideOptimismEcotone
	}
	if o.OverrideOptimismFjord != nil {
		cpy.FjordTime = o.OverrideOptimismFjord
	}
	if o.OverrideOptimismGranite != nil {
		cpy.GraniteTime = o.OverrideOptimismGranite
	}
	if o.OverrideOptimismHolocene != nil {
		cpy.HoloceneTime = o.OverrideOptimismHolocene
	}
	if o.OverrideOptimismInterop != nil {
		cpy.InteropTime = o.OverrideOptimismInterop
	}

	if err := cpy.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	return &cpy, nil
}

// SetupGenesisBlock writes or updates the genesis block in db.
// The block that will be used is:
//
//	                     genesis == nil       genesis != nil
//	                  +------------------------------------------
//	db has no genesis |  main-net default  |  genesis
//	db has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *params.ConfigCompatError and the new, unwritten config is returned.
func SetupGenesisBlock(db ethdb.Database, triedb *triedb.Database, genesis *Genesis) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
	return SetupGenesisBlockWithOverride(db, triedb, genesis, nil)
}

func SetupGenesisBlockWithOverride(db ethdb.Database, triedb *triedb.Database, genesis *Genesis, overrides *ChainOverrides) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
	// Sanitize the supplied genesis, ensuring it has the associated chain
	// config attached.
	if genesis != nil && genesis.Config == nil {
		return nil, common.Hash{}, nil, errGenesisNoConfig
	}
	// Commit the genesis if the database is empty
	ghash := rawdb.ReadCanonicalHash(db, 0)
	if (ghash == common.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		chainCfg, err := overrides.apply(genesis.Config)
		if err != nil {
			return nil, common.Hash{}, nil, err
		}
		genesis.Config = chainCfg

		block, err := genesis.Commit(db, triedb)
		if err != nil {
			return nil, common.Hash{}, nil, err
		}
		return chainCfg, block.Hash(), nil, nil
	}

	// TODO: Reinstate transitioned network check. Not sure where exactly to put it after this refactor.
	// If the bedrock block is not 0, that implies that the network was migrated at the bedrock block.
	// In this case the genesis state may not be in the state database (e.g. op-geth is performing a snap
	// sync without an existing datadir) & even if it were, would not be useful as op-geth is not able to
	// execute the pre-bedrock STF.
	// transitionedNetwork := genesis != nil && genesis.Config != nil && genesis.Config.BedrockBlock != nil && genesis.Config.BedrockBlock.Uint64() != 0

	// Commit the genesis if the genesis block exists in the ancient database
	// but the key-value database is empty without initializing the genesis
	// fields. This scenario can occur when the node is created from scratch
	// with an existing ancient store.
	storedCfg := rawdb.ReadChainConfig(db, ghash)
	if storedCfg == nil {
		// Ensure the stored genesis block matches with the given genesis. Private
		// networks must explicitly specify the genesis in the config file, mainnet
		// genesis will be used as default and the initialization will always fail.
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		chainCfg, err := overrides.apply(genesis.Config)
		if err != nil {
			return nil, common.Hash{}, nil, err
		}
		genesis.Config = chainCfg

		if hash := genesis.ToBlock().Hash(); hash != ghash {
			return nil, common.Hash{}, nil, &GenesisMismatchError{ghash, hash}
		}
		block, err := genesis.Commit(db, triedb)
		if err != nil {
			return nil, common.Hash{}, nil, err
		}
		return chainCfg, block.Hash(), nil, nil
	}
	log.Info("Stored config", "cfg", storedCfg, "genesis-nil", genesis == nil)
	// The genesis block has already been committed previously. Verify that the
	// provided genesis with chain overrides matches the existing one, and update
	// the stored chain config if necessary.
	if genesis != nil {
		chainCfg, err := overrides.apply(genesis.Config)
		if err != nil {
			return nil, common.Hash{}, nil, err
		}
		genesis.Config = chainCfg

		if hash := genesis.ToBlock().Hash(); hash != ghash {
			return nil, common.Hash{}, nil, &GenesisMismatchError{ghash, hash}
		}
	}
	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	head := rawdb.ReadHeadHeader(db)
	if head == nil {
		return nil, common.Hash{}, nil, errors.New("missing head header")
	}
	newCfg := genesis.chainConfigOrDefault(ghash, storedCfg)

	log.Info("New config", "cfg", newCfg, "genesis-nil", genesis == nil)
	var genesisTimestamp *uint64
	if genesis != nil {
		genesisTimestamp = &genesis.Timestamp
	}

	// TODO(rjl493456442) better to define the comparator of chain config
	// and short circuit if the chain config is not changed.
	compatErr := storedCfg.CheckCompatible(newCfg, head.Number.Uint64(), head.Time, genesisTimestamp)
	if compatErr != nil && ((head.Number.Uint64() != 0 && compatErr.RewindToBlock != 0) || (head.Time != 0 && compatErr.RewindToTime != 0)) {
		return newCfg, ghash, compatErr, nil
	}

	// Don't overwrite if the old is identical to the new. It's useful
	// for the scenarios that database is opened in the read-only mode.
	storedData, _ := json.Marshal(storedCfg)
	if newData, _ := json.Marshal(newCfg); !bytes.Equal(storedData, newData) {
		log.Info("Configs differ")
		rawdb.WriteChainConfig(db, ghash, newCfg)
	} else {
		log.Info("Configs equal")
	}
	return newCfg, ghash, nil, nil
}

// LoadChainConfig loads the stored chain config if it is already present in
// database, otherwise, return the config in the provided genesis specification.
func LoadChainConfig(db ethdb.Database, genesis *Genesis) (*params.ChainConfig, error) {
	// Load the stored chain config from the database. It can be nil
	// in case the database is empty. Notably, we only care about the
	// chain config corresponds to the canonical chain.
	stored := rawdb.ReadCanonicalHash(db, 0)
	if stored != (common.Hash{}) {
		storedcfg := rawdb.ReadChainConfig(db, stored)
		if storedcfg != nil {
			return storedcfg, nil
		}
	}
	// Load the config from the provided genesis specification
	if genesis != nil {
		// Reject invalid genesis spec without valid chain config
		if genesis.Config == nil {
			return nil, errGenesisNoConfig
		}
		// If the canonical genesis header is present, but the chain
		// config is missing(initialize the empty leveldb with an
		// external ancient chain segment), ensure the provided genesis
		// is matched.
		if stored != (common.Hash{}) && genesis.ToBlock().Hash() != stored {
			return nil, &GenesisMismatchError{stored, genesis.ToBlock().Hash()}
		}
		return genesis.Config, nil
	}
	// There is no stored chain config and no new config provided,
	// In this case the default chain config(mainnet) will be used
	return params.MainnetChainConfig, nil
}

// chainConfigOrDefault retrieves the attached chain configuration. If the genesis
// object is null, it returns the default chain configuration based on the given
// genesis hash, or the locally stored config if it's not a pre-defined network.
func (g *Genesis) chainConfigOrDefault(ghash common.Hash, stored *params.ChainConfig) *params.ChainConfig {
	switch {
	case g != nil:
		return g.Config
	case ghash == params.MainnetGenesisHash:
		return params.MainnetChainConfig
	case ghash == params.HoleskyGenesisHash:
		return params.HoleskyChainConfig
	case ghash == params.SepoliaGenesisHash:
		return params.SepoliaChainConfig
	default:
		return stored
	}
}

// IsVerkle indicates whether the state is already stored in a verkle
// tree at genesis time.
func (g *Genesis) IsVerkle() bool {
	return g.Config.IsVerkleGenesis()
}

// ToBlock returns the genesis block according to genesis specification.
func (g *Genesis) ToBlock() *types.Block {
	var stateRoot, storageRootMessagePasser common.Hash
	var err error
	if g.StateHash != nil {
		if len(g.Alloc) > 0 {
			panic(fmt.Errorf("cannot both have genesis hash %s "+
				"and non-empty state-allocation", *g.StateHash))
		}
		// g.StateHash is only relevant for pre-bedrock (and hence pre-isthmus) chains.
		// we bail here since this is not a valid usage of StateHash
		if g.Config.IsOptimismIsthmus(g.Timestamp) {
			panic(fmt.Errorf("stateHash usage disallowed in chain with isthmus active at genesis"))
		}
		stateRoot = *g.StateHash
	} else if stateRoot, storageRootMessagePasser, err = hashAlloc(&g.Alloc, g.IsVerkle(), g.Config.IsOptimismIsthmus(g.Timestamp)); err != nil {
		panic(err)
	}
	return g.toBlockWithRoot(stateRoot, storageRootMessagePasser)
}

// toBlockWithRoot constructs the genesis block with the given genesis state root.
func (g *Genesis) toBlockWithRoot(stateRoot, storageRootMessagePasser common.Hash) *types.Block {
	head := &types.Header{
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      types.EncodeNonce(g.Nonce),
		Time:       g.Timestamp,
		ParentHash: g.ParentHash,
		Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		BaseFee:    g.BaseFee,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
		Root:       stateRoot,
	}
	if g.GasLimit == 0 {
		head.GasLimit = params.GenesisGasLimit
	}
	if g.Difficulty == nil && g.Mixhash == (common.Hash{}) {
		head.Difficulty = params.GenesisDifficulty
	}
	if g.Config != nil && g.Config.IsLondon(common.Big0) {
		if g.BaseFee != nil {
			head.BaseFee = g.BaseFee
		} else {
			head.BaseFee = new(big.Int).SetUint64(params.InitialBaseFee)
		}
	}
	var withdrawals []*types.Withdrawal
	if conf := g.Config; conf != nil {
		num := big.NewInt(int64(g.Number))
		if conf.IsShanghai(num, g.Timestamp) {
			head.WithdrawalsHash = &types.EmptyWithdrawalsHash
			withdrawals = make([]*types.Withdrawal, 0)
		}
		if conf.IsCancun(num, g.Timestamp) {
			// EIP-4788: The parentBeaconBlockRoot of the genesis block is always
			// the zero hash. This is because the genesis block does not have a parent
			// by definition.
			head.ParentBeaconRoot = new(common.Hash)
			// EIP-4844 fields
			head.ExcessBlobGas = g.ExcessBlobGas
			head.BlobGasUsed = g.BlobGasUsed
			if head.ExcessBlobGas == nil {
				head.ExcessBlobGas = new(uint64)
			}
			if head.BlobGasUsed == nil {
				head.BlobGasUsed = new(uint64)
			}
		}
		if conf.IsPrague(num, g.Timestamp) {
			head.RequestsHash = &types.EmptyRequestsHash
		}
		// If Isthmus is active at genesis, set the WithdrawalRoot to the storage root of the L2ToL1MessagePasser contract.
		if g.Config.IsOptimismIsthmus(g.Timestamp) {
			if storageRootMessagePasser == (common.Hash{}) {
				// if there was no MessagePasser contract storage, set the WithdrawalsHash to the empty hash
				storageRootMessagePasser = types.EmptyWithdrawalsHash
			}
			head.WithdrawalsHash = &storageRootMessagePasser
		}
	}
	return types.NewBlock(head, &types.Body{Withdrawals: withdrawals}, nil, trie.NewStackTrie(nil), g.Config)
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db ethdb.Database, triedb *triedb.Database) (*types.Block, error) {
	if g.Number != 0 {
		return nil, errors.New("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		return nil, errors.New("invalid genesis without chain config")
	}
	if err := config.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	if config.Clique != nil && len(g.ExtraData) < 32+crypto.SignatureLength {
		return nil, errors.New("can't start clique chain without signers")
	}
	var stateRoot, storageRootMessagePasser common.Hash
	var err error
	if len(g.Alloc) == 0 {
		if g.StateHash == nil {
			log.Warn("Empty genesis alloc, and no 'stateHash' override was set")
			stateRoot = types.EmptyRootHash // default to the hash of the empty state. Some unit-tests rely on this.
		} else {
			stateRoot = *g.StateHash
		}
	} else {
		// flush the data to disk and compute the state root
		stateRoot, storageRootMessagePasser, err = flushAlloc(&g.Alloc, triedb, g.Config.IsIsthmus(g.Timestamp))
		if err != nil {
			return nil, err
		}
	}
	block := g.toBlockWithRoot(stateRoot, storageRootMessagePasser)

	// Marshal the genesis state specification and persist.
	blob, err := json.Marshal(g.Alloc)
	if err != nil {
		return nil, err
	}
	batch := db.NewBatch()
	rawdb.WriteGenesisStateSpec(batch, block.Hash(), blob)
	rawdb.WriteTd(batch, block.Hash(), block.NumberU64(), block.Difficulty())
	rawdb.WriteBlock(batch, block)
	rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), nil)
	rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(batch, block.Hash())
	rawdb.WriteHeadFastBlockHash(batch, block.Hash())
	rawdb.WriteHeadHeaderHash(batch, block.Hash())
	rawdb.WriteChainConfig(batch, block.Hash(), config)
	return block, batch.Write()
}

// MustCommit writes the genesis block and state to db, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db ethdb.Database, triedb *triedb.Database) *types.Block {
	block, err := g.Commit(db, triedb)
	if err != nil {
		panic(err)
	}
	return block
}

// EnableVerkleAtGenesis indicates whether the verkle fork should be activated
// at genesis. This is a temporary solution only for verkle devnet testing, where
// verkle fork is activated at genesis, and the configured activation date has
// already passed.
//
// In production networks (mainnet and public testnets), verkle activation always
// occurs after the genesis block, making this function irrelevant in those cases.
func EnableVerkleAtGenesis(db ethdb.Database, genesis *Genesis) (bool, error) {
	if genesis != nil {
		if genesis.Config == nil {
			return false, errGenesisNoConfig
		}
		return genesis.Config.EnableVerkleAtGenesis, nil
	}
	if ghash := rawdb.ReadCanonicalHash(db, 0); ghash != (common.Hash{}) {
		chainCfg := rawdb.ReadChainConfig(db, ghash)
		if chainCfg != nil {
			return chainCfg.EnableVerkleAtGenesis, nil
		}
	}
	return false, nil
}

// DefaultGenesisBlock returns the Ethereum main net genesis block.
func DefaultGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.MainnetChainConfig,
		Nonce:      66,
		ExtraData:  hexutil.MustDecode("0x11bbe8db4e347b4e8c937c1c8370e4b5ed33adb3db69cbdb7a38e1e50b1b82fa"),
		GasLimit:   5000,
		Difficulty: big.NewInt(17179869184),
		Alloc:      decodePrealloc(mainnetAllocData),
	}
}

// DefaultSepoliaGenesisBlock returns the Sepolia network genesis block.
func DefaultSepoliaGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.SepoliaChainConfig,
		Nonce:      0,
		ExtraData:  []byte("Sepolia, Athens, Attica, Greece!"),
		GasLimit:   0x1c9c380,
		Difficulty: big.NewInt(0x20000),
		Timestamp:  1633267481,
		Alloc:      decodePrealloc(sepoliaAllocData),
	}
}

// DefaultHoleskyGenesisBlock returns the Holesky network genesis block.
func DefaultHoleskyGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.HoleskyChainConfig,
		Nonce:      0x1234,
		GasLimit:   0x17d7840,
		Difficulty: big.NewInt(0x01),
		Timestamp:  1695902100,
		Alloc:      decodePrealloc(holeskyAllocData),
	}
}

// DeveloperGenesisBlock returns the 'geth --dev' genesis block.
func DeveloperGenesisBlock(gasLimit uint64, faucet *common.Address) *Genesis {
	// Override the default period to the user requested one
	config := *params.AllDevChainProtocolChanges

	// Assemble and return the genesis with the precompiles and faucet pre-funded
	genesis := &Genesis{
		Config:     &config,
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(0),
		Alloc: map[common.Address]types.Account{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			common.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			// Pre-deploy system contracts
			params.BeaconRootsAddress:        {Nonce: 1, Code: params.BeaconRootsCode, Balance: common.Big0},
			params.HistoryStorageAddress:     {Nonce: 1, Code: params.HistoryStorageCode, Balance: common.Big0},
			params.WithdrawalQueueAddress:    {Nonce: 1, Code: params.WithdrawalQueueCode, Balance: common.Big0},
			params.ConsolidationQueueAddress: {Nonce: 1, Code: params.ConsolidationQueueCode, Balance: common.Big0},
		},
	}
	if faucet != nil {
		genesis.Alloc[*faucet] = types.Account{Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))}
	}
	return genesis
}

func decodePrealloc(data string) types.GenesisAlloc {
	var p []struct {
		Addr    *big.Int
		Balance *big.Int
		Misc    *struct {
			Nonce uint64
			Code  []byte
			Slots []struct {
				Key common.Hash
				Val common.Hash
			}
		} `rlp:"optional"`
	}
	if err := rlp.NewStream(strings.NewReader(data), 0).Decode(&p); err != nil {
		panic(err)
	}
	ga := make(types.GenesisAlloc, len(p))
	for _, account := range p {
		acc := types.Account{Balance: account.Balance}
		if account.Misc != nil {
			acc.Nonce = account.Misc.Nonce
			acc.Code = account.Misc.Code

			acc.Storage = make(map[common.Hash]common.Hash)
			for _, slot := range account.Misc.Slots {
				acc.Storage[slot.Key] = slot.Val
			}
		}
		ga[common.BigToAddress(account.Addr)] = acc
	}
	return ga
}
