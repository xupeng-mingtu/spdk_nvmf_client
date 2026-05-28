package tcp

import (
	"fmt"
	"log/slog"
	"time"
)

// ClientConfig 是 NVMe-oF TCP 客户端的配置参数
type ClientConfig struct {
	Addr           string
	HostNQN        string
	SubNQN         string
	NSID           uint32
	BlockSize      uint32
	ConnectTimeout time.Duration
}

const DefaultHostNQN = "nqn.2014-08.org.nvmexpress:uuid:43c48637-f550-4f06-8d28-3870c9604dd2"

// Client 是 NVMe-oF TCP 客户端
type Client struct {
	cfg     ClientConfig
	adminQP *qpair
	ioQP    *qpair
}

// NewClient 创建并初始化 NVMe-oF TCP 客户端
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.NSID == 0 {
		cfg.NSID = 1
	}
	if cfg.BlockSize == 0 {
		cfg.BlockSize = defaultBlockSize
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 3000 * time.Second
	}
	if cfg.HostNQN == "" {
		cfg.HostNQN = DefaultHostNQN
	}

	c := &Client{cfg: cfg}

	c.adminQP = newAdminQpair()
	if err := c.adminQP.connect(cfg.Addr, cfg.HostNQN, cfg.SubNQN); err != nil {
		return nil, fmt.Errorf("connect admin qpair: %w", err)
	}

	if err := c.adminQP.enableController(); err != nil {
		c.adminQP.close()
		return nil, fmt.Errorf("enable controller: %w", err)
	}

	maxCapsuleDataSize, err := c.adminQP.queryMaxCapsuleDataSize()
	if err != nil {
		c.adminQP.close()
		return nil, fmt.Errorf("query max capsule data size: %w", err)
	}
	slog.Info("queried max capsule data size", "bytes", maxCapsuleDataSize)

	c.ioQP = newIOQpair(c.adminQP.ctrlID)
	c.ioQP.maxCapsuleDataSize = maxCapsuleDataSize
	if err := c.ioQP.connect(cfg.Addr, cfg.HostNQN, cfg.SubNQN); err != nil {
		c.adminQP.close()
		return nil, fmt.Errorf("connect io qpair: %w", err)
	}

	return c, nil
}

func (c *Client) Close() {
	if c.ioQP != nil {
		c.ioQP.close()
		c.ioQP = nil
	}
	if c.adminQP != nil {
		c.adminQP.close()
		c.adminQP = nil
	}
}

func (c *Client) Read(lba uint64, lbaCount uint32) ([]byte, error) {
	return c.ioQP.Read(lba, lbaCount, c.cfg.NSID, c.cfg.BlockSize)
}

func (c *Client) Write(lba uint64, data []byte) error {
	return c.ioQP.Write(lba, c.cfg.NSID, c.cfg.BlockSize, data)
}

func (c *Client) WriteZeroes(lba uint64, lbaCount uint32) error {
	return c.ioQP.WriteZeroes(lba, lbaCount, c.cfg.NSID)
}

func (c *Client) Unmap(ranges []UnmapRange) error {
	return c.ioQP.Unmap(c.cfg.NSID, ranges)
}

func (c *Client) NSID() uint32 {
	return c.cfg.NSID
}

func (c *Client) BlockSize() uint32 {
	return c.cfg.BlockSize
}
