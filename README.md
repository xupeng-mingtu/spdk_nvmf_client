# SPDK NVMe-oF TCP Client

一个纯 Go 实现的 NVMe-oF (NVMe over Fabrics) TCP 客户端库，零外部依赖，仅使用 Go 标准库。

## 功能特性

- 纯 Go 实现，无 CGO 依赖
- 支持 NVMe-oF TCP 协议（基于 NVMe-oF 1.1 规范）
- 支持基本的 NVMe I/O 操作：
  - `Read` — 从指定 LBA 读取数据
  - `Write` — 向指定 LBA 写入数据
  - `WriteZeroes` — 将指定 LBA 范围写零
  - `Unmap` — 执行 Dataset Management (Deallocate) 操作
- 自动处理协议层细节（ICReq/ICResp、Fabric Connect、R2T、C2HData、H2CData、Capsule PDU）
- 支持 in-capsule 小数据优化（≤4KB 直接封装，无需 R2T 往返）

## 环境要求

- **Go 1.21+**（因使用标准库 `log/slog`）

## 安装

```bash
go get github.com/xupeng-mingtu/spdk_nvmf_client/tcp
```

## 快速开始

```go
package main

import (
    "fmt"
    "log"
    "time"

    "github.com/xupeng-mingtu/spdk_nvmf_client/tcp"
)

func main() {
    client, err := tcp.NewClient(tcp.ClientConfig{
        Addr:           "192.168.1.100:4420",
        HostNQN:        "nqn.2014-08.org.nvmexpress:uuid:myhost",
        SubNQN:         "nqn.2016-06.io.spdk:cnode1",
        NSID:           1,
        ConnectTimeout: 10 * time.Second,
    })
    if err != nil {
        log.Fatalf("connect failed: %v", err)
    }
    defer client.Close()

    // 写入数据
    data := make([]byte, 4096)
    if err := client.Write(0, data); err != nil {
        log.Fatalf("write failed: %v", err)
    }

    // 读取数据
    readBuf, err := client.Read(0, 8) // 读取 8 个 block (4096 字节)
    if err != nil {
        log.Fatalf("read failed: %v", err)
    }
    fmt.Printf("read %d bytes\n", len(readBuf))
}
```

## API 说明

### `ClientConfig`

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `Addr` | `string` | NVMe-oF Target 地址，如 `host:4420` | 必填 |
| `HostNQN` | `string` | Host NQN (NVMe Qualified Name) | 内置默认 UUID |
| `SubNQN` | `string` | Subsystem NQN，目标端的子系统名称 | 必填 |
| `NSID` | `uint32` | Namespace ID | `1` |
| `ConnectTimeout` | `time.Duration` | 连接超时时间 | `3000s` |

> **注意**：块大小（`BlockSize`）不再通过配置指定。客户端在连接建立后会自动通过 NVMe Identify Namespace 命令查询目标 Namespace 的实际 LBA 格式并计算块大小，可通过 `client.BlockSize()` 获取。

### `Client` 方法

- `func NewClient(cfg ClientConfig) (*Client, error)` — 创建客户端并建立 Admin 和 I/O Queue Pair 连接
- `func (c *Client) Close()` — 关闭连接
- `func (c *Client) Read(lba uint64, lbaCount uint32) ([]byte, error)` — 读取指定 LBA 范围的块数据
- `func (c *Client) Write(lba uint64, data []byte) error` — 写入数据（长度必须是 BlockSize 的整数倍）
- `func (c *Client) WriteZeroes(lba uint64, lbaCount uint32) error` — 将指定范围写零
- `func (c *Client) Unmap(ranges []UnmapRange) error` — 解除映射（最多 256 个 range）
- `func (c *Client) NSID() uint32` — 返回当前 Namespace ID
- `func (c *Client) BlockSize() uint32` — 返回当前块大小

## 构建

本项目附带 [`Makefile`](Makefile:1)，可一键编译示例工具：

```bash
# 编译示例工具，输出到 build/tcp-nvmf-io
make

# 清理构建产物
make clean
```

或直接通过 Go 工具链编译：

```bash
go build ./...
go run ./examples/tcp-nvmf-io ...
```

## 命令行工具示例

项目附带了一个命令行示例程序 [`examples/tcp-nvmf-io/main.go`](examples/tcp-nvmf-io/main.go:1)，可直接用于测试 NVMe-oF Target：

```bash
# 编译
make

# 运行示例
./build/tcp-nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 write -lba 0 -count 8
./build/tcp-nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 read  -lba 0 -count 8
./build/tcp-nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 rw    -lba 0 -count 8
```

支持命令：`write`、`read`、`wzero`、`unmap`、`rw`。使用 `-debug` 可开启详细日志。

## 目录结构

```
tcp/
├── client.go   # 客户端入口与生命周期管理
├── conn.go     # TCP 连接与 PDU 收发逻辑
├── io.go       # NVMe 命令构建与 I/O 方法（Read/Write/Unmap）
├── qpair.go    # Queue Pair 管理与 Fabric Connect 流程
└── types.go    # NVMe-oF TCP PDU 类型、SGL、常量定义

examples/
└── tcp-nvmf-io/
    └── main.go # 命令行 IO 测试工具示例
```

## 协议实现细节

- **连接建立**：TCP Dial → ICReq/ICResp → Fabric Connect (Admin Queue) → Property Set (CC.EN=1) → Fabric Connect (I/O Queue)
- **数据传输方式**：
  - 小数据（≤4KB）：使用 in-capsule Data Block with Offset SGL，直接随 Capsule Command 发送
  - 大数据：使用 Transport SGL，通过 R2T → H2CData 流程传输写数据，或通过 C2HData 接收读数据
- **命令标识**：每个 Queue Pair 内部维护递增的 16-bit CID

## 依赖

仅依赖 Go 标准库，**零外部依赖**，因此项目不包含 `go.sum`：

- `encoding/binary`
- `fmt`
- `io`
- `log/slog`
- `net`
- `time`

## 许可证

MIT License
