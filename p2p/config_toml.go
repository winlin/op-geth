// Code generated by github.com/fjl/gencodec. DO NOT EDIT.

package p2p

import (
	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/nat"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

var _ = (*configMarshaling)(nil)

// MarshalTOML marshals as TOML.
func (c Config) MarshalTOML() (interface{}, error) {
	type Config struct {
		PrivateKey       *ecdsa.PrivateKey `toml:"-"`
		MaxPeers         int
		MaxPendingPeers  int `toml:",omitempty"`
		DialRatio        int `toml:",omitempty"`
		NoDiscovery      bool
		DiscoveryV4      bool   `toml:",omitempty"`
		DiscoveryV5      bool   `toml:",omitempty"`
		Name             string `toml:"-"`
		BootstrapNodes   []*enode.Node
		BootstrapNodesV5 []*enode.Node `toml:",omitempty"`
		StaticNodes      []*enode.Node
		TrustedNodes     []*enode.Node
		NetRestrict      *netutil.Netlist `toml:",omitempty"`
		NodeDatabase     string           `toml:",omitempty"`
		Protocols        []Protocol       `toml:"-" json:"-"`
		ListenAddr       string
		DiscAddr         string
		NAT              nat.Interface `toml:",omitempty"`
		Dialer           NodeDialer    `toml:"-"`
		NoDial           bool          `toml:",omitempty"`
		EnableMsgEvents  bool
		Logger           log.Logger `toml:"-"`
	}
	var enc Config
	enc.PrivateKey = c.PrivateKey
	enc.MaxPeers = c.MaxPeers
	enc.MaxPendingPeers = c.MaxPendingPeers
	enc.DialRatio = c.DialRatio
	enc.NoDiscovery = c.NoDiscovery
	enc.DiscoveryV4 = c.DiscoveryV4
	enc.DiscoveryV5 = c.DiscoveryV5
	enc.Name = c.Name
	enc.BootstrapNodes = c.BootstrapNodes
	enc.BootstrapNodesV5 = c.BootstrapNodesV5
	enc.StaticNodes = c.StaticNodes
	enc.TrustedNodes = c.TrustedNodes
	enc.NetRestrict = c.NetRestrict
	enc.NodeDatabase = c.NodeDatabase
	enc.Protocols = c.Protocols
	enc.ListenAddr = c.ListenAddr
	enc.DiscAddr = c.DiscAddr
	enc.NAT = c.NAT
	enc.Dialer = c.Dialer
	enc.NoDial = c.NoDial
	enc.EnableMsgEvents = c.EnableMsgEvents
	enc.Logger = c.Logger
	return &enc, nil
}

// UnmarshalTOML unmarshals from TOML.
func (c *Config) UnmarshalTOML(unmarshal func(interface{}) error) error {
	type Config struct {
		PrivateKey       *ecdsa.PrivateKey `toml:"-"`
		MaxPeers         *int
		MaxPendingPeers  *int `toml:",omitempty"`
		DialRatio        *int `toml:",omitempty"`
		NoDiscovery      *bool
		DiscoveryV4      *bool   `toml:",omitempty"`
		DiscoveryV5      *bool   `toml:",omitempty"`
		Name             *string `toml:"-"`
		BootstrapNodes   []*enode.Node
		BootstrapNodesV5 []*enode.Node `toml:",omitempty"`
		StaticNodes      []*enode.Node
		TrustedNodes     []*enode.Node
		NetRestrict      *netutil.Netlist `toml:",omitempty"`
		NodeDatabase     *string          `toml:",omitempty"`
		Protocols        []Protocol       `toml:"-" json:"-"`
		ListenAddr       *string
		DiscAddr         *string
		NAT              *configNAT `toml:",omitempty"`
		Dialer           NodeDialer `toml:"-"`
		NoDial           *bool      `toml:",omitempty"`
		EnableMsgEvents  *bool
		Logger           log.Logger `toml:"-"`
	}
	var dec Config
	if err := unmarshal(&dec); err != nil {
		return err
	}
	if dec.PrivateKey != nil {
		c.PrivateKey = dec.PrivateKey
	}
	if dec.MaxPeers != nil {
		c.MaxPeers = *dec.MaxPeers
	}
	if dec.MaxPendingPeers != nil {
		c.MaxPendingPeers = *dec.MaxPendingPeers
	}
	if dec.DialRatio != nil {
		c.DialRatio = *dec.DialRatio
	}
	if dec.NoDiscovery != nil {
		c.NoDiscovery = *dec.NoDiscovery
	}
	if dec.DiscoveryV4 != nil {
		c.DiscoveryV4 = *dec.DiscoveryV4
	}
	if dec.DiscoveryV5 != nil {
		c.DiscoveryV5 = *dec.DiscoveryV5
	}
	if dec.Name != nil {
		c.Name = *dec.Name
	}
	if dec.BootstrapNodes != nil {
		c.BootstrapNodes = dec.BootstrapNodes
	}
	if dec.BootstrapNodesV5 != nil {
		c.BootstrapNodesV5 = dec.BootstrapNodesV5
	}
	if dec.StaticNodes != nil {
		c.StaticNodes = dec.StaticNodes
	}
	if dec.TrustedNodes != nil {
		c.TrustedNodes = dec.TrustedNodes
	}
	if dec.NetRestrict != nil {
		c.NetRestrict = dec.NetRestrict
	}
	if dec.NodeDatabase != nil {
		c.NodeDatabase = *dec.NodeDatabase
	}
	if dec.Protocols != nil {
		c.Protocols = dec.Protocols
	}
	if dec.ListenAddr != nil {
		c.ListenAddr = *dec.ListenAddr
	}
	if dec.DiscAddr != nil {
		c.DiscAddr = *dec.DiscAddr
	}
	if dec.NAT != nil {
		c.NAT = dec.NAT
	}
	if dec.Dialer != nil {
		c.Dialer = dec.Dialer
	}
	if dec.NoDial != nil {
		c.NoDial = *dec.NoDial
	}
	if dec.EnableMsgEvents != nil {
		c.EnableMsgEvents = *dec.EnableMsgEvents
	}
	if dec.Logger != nil {
		c.Logger = dec.Logger
	}
	return nil
}
