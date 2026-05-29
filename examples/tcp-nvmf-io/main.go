// nvmf-io 是一个基于 NVMe-oF TCP 的简单 IO 测试工具
//
// 用法：
//
//	nvmf-io -addr <host:port> -subnqn <nqn> [选项] <命令>
//
// 命令：
//
//	write    向指定 LBA 写入数据（默认填充 0x5A 模式）
//	read     从指定 LBA 读取数据并打印十六进制摘要
//	wzero    对指定 LBA 范围执行 Write Zeroes
//	unmap    对指定 LBA 范围执行 Unmap（Dataset Management Deallocate）
//	rw       先写后读并验证数据一致性
//
// 示例：
//
//	# 写入 LBA 0 开始的 8 个块（4KB）
//	nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 write -lba 0 -count 8
//
//	# 读取 LBA 0 开始的 8 个块
//	nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 read -lba 0 -count 8
//
//	# 写零
//	nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 wzero -lba 0 -count 8
//
//	# Unmap
//	nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 unmap -lba 0 -count 8
//
//	# 写后读验证
//	nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 rw -lba 0 -count 8
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xupeng-mingtu/spdk_nvmf_client/tcp"
)

var (
	flagAddr    = flag.String("addr", "", "NVMe-oF target 地址，格式 host:port（必填）")
	flagSubNQN  = flag.String("subnqn", "", "目标 subsystem NQN（必填）")
	flagHostNQN = flag.String("hostnqn", tcp.DefaultHostNQN, "本端 host NQN")
	flagNSID    = flag.Uint("nsid", 1, "命名空间 ID")
	flagDebug   = flag.Bool("debug", false, "启用 debug 日志级别")
)

// setupDebugLogger 设置 slog 为 debug 级别输出到标准错误
// setupLogger 设置 slog 输出到标准错误，根据 debug 模式调整日志级别
func setupLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					// 只保留文件名，去掉路径
					filename := filepath.Base(src.File)
					a.Value = slog.StringValue(filename + ":" + strconv.Itoa(src.Line))
				}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
}
func usage() {
	fmt.Fprintf(os.Stderr, `用法：nvmf-io [全局选项] <命令> [命令选项]

全局选项：
  -addr      string   NVMe-oF target 地址，格式 host:port（必填）
  -subnqn    string   目标 subsystem NQN（必填）
  -hostnqn   string   本端 host NQN（默认：%s）
  -nsid      uint     命名空间 ID（默认：1）
  -debug     bool     启用 debug 日志级别（默认：false）

命令：
  write   向指定 LBA 写入数据
            -lba    uint   起始 LBA（默认：0）
            -count  uint   块数（默认：8）
            -fill   uint   填充字节值 0-255（默认：0x5A）
  read    从指定 LBA 读取数据
            -lba    uint   起始 LBA（默认：0）
            -count  uint   块数（默认：8）
  wzero   对指定 LBA 范围执行 Write Zeroes
            -lba    uint   起始 LBA（默认：0）
            -count  uint   块数（默认：8）
  unmap   对指定 LBA 范围执行 Unmap
            -lba    uint   起始 LBA（默认：0）
            -count  uint   块数（默认：8）
  rw      先写后读并验证数据一致性
            -lba    uint   起始 LBA（默认：0）
            -count  uint   块数（默认：8）
            -fill   uint   填充字节值 0-255（默认：0x5A）

示例：
  nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 write -lba 0 -count 8
  nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 read  -lba 0 -count 8
  nvmf-io -addr 192.168.1.10:4420 -subnqn nqn.2022-08.io.spdk:vol1 rw    -lba 0 -count 8
`, tcp.DefaultHostNQN)
}

func main() {
	flag.Usage = usage
	flag.Parse()

	setupLogger(*flagDebug)

	if *flagAddr == "" || *flagSubNQN == "" {
		fmt.Fprintln(os.Stderr, "错误：-addr 和 -subnqn 为必填参数")
		usage()
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "错误：请指定命令（write/read/wzero/unmap/rw）")
		usage()
		os.Exit(1)
	}

	cmd := strings.ToLower(args[0])
	cmdArgs := args[1:]

	// 建立 NVMe-oF TCP 连接
	slog.Info("连接到 NVMe-oF target", "addr", *flagAddr, "subnqn", *flagSubNQN)
	client, err := tcp.NewClient(tcp.ClientConfig{
		Addr:    *flagAddr,
		HostNQN: *flagHostNQN,
		SubNQN:  *flagSubNQN,
		NSID:    uint32(*flagNSID),
	})
	if err != nil {
		slog.Error("连接失败", "error", err)
		os.Exit(1)
	}
	defer client.Close()
	slog.Info("连接成功", "nsid", client.NSID(), "block_size", client.BlockSize())

	// 执行命令
	var cmdErr error
	switch cmd {
	case "write":
		cmdErr = runWrite(client, cmdArgs)
	case "read":
		cmdErr = runRead(client, cmdArgs)
	case "wzero":
		cmdErr = runWriteZeroes(client, cmdArgs)
	case "unmap":
		cmdErr = runUnmap(client, cmdArgs)
	case "rw":
		cmdErr = runRW(client, cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n", cmd)
		usage()
		os.Exit(1)
	}

	if cmdErr != nil {
		slog.Error("命令执行失败", "command", cmd, "error", cmdErr)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------
// write 命令
// -----------------------------------------------------------------------

func runWrite(client *tcp.Client, args []string) error {
	fs := flag.NewFlagSet("write", flag.ExitOnError)
	lba := fs.Uint64("lba", 0, "起始 LBA")
	count := fs.Uint("count", 8, "块数")
	fill := fs.Uint("fill", 0x5A, "填充字节值（0-255）")
	fs.Parse(args)

	totalBytes := uint64(*count) * uint64(client.BlockSize())
	data := make([]byte, totalBytes)
	fillByte := byte(*fill & 0xFF)
	for i := range data {
		data[i] = fillByte
	}

	slog.Info("写入", "lba", *lba, "count", *count, "total_bytes", totalBytes, "fill", fmt.Sprintf("0x%02X", fillByte))

	if err := client.Write(*lba, data); err != nil {
		return err
	}
	slog.Info("写入成功")
	return nil
}

// -----------------------------------------------------------------------
// read 命令
// -----------------------------------------------------------------------

func runRead(client *tcp.Client, args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	lba := fs.Uint64("lba", 0, "起始 LBA")
	count := fs.Uint("count", 8, "块数")
	fs.Parse(args)

	totalBytes := uint64(*count) * uint64(client.BlockSize())
	slog.Info("读取", "lba", *lba, "count", *count, "total_bytes", totalBytes)

	buf, err := client.Read(*lba, uint32(*count))
	if err != nil {
		return err
	}

	slog.Info("读取成功")
	printHexSummary(buf)
	return nil
}

// -----------------------------------------------------------------------
// wzero 命令
// -----------------------------------------------------------------------

func runWriteZeroes(client *tcp.Client, args []string) error {
	fs := flag.NewFlagSet("wzero", flag.ExitOnError)
	lba := fs.Uint64("lba", 0, "起始 LBA")
	count := fs.Uint("count", 8, "块数")
	fs.Parse(args)

	slog.Info("写零", "lba", *lba, "count", *count)

	if err := client.WriteZeroes(*lba, uint32(*count)); err != nil {
		return err
	}
	slog.Info("写零成功")
	return nil
}

// -----------------------------------------------------------------------
// unmap 命令
// -----------------------------------------------------------------------

func runUnmap(client *tcp.Client, args []string) error {
	fs := flag.NewFlagSet("unmap", flag.ExitOnError)
	lba := fs.Uint64("lba", 0, "起始 LBA")
	count := fs.Uint("count", 8, "块数")
	fs.Parse(args)

	slog.Info("Unmap", "lba", *lba, "count", *count)

	ranges := []tcp.UnmapRange{
		{StartLBA: *lba, LBACount: uint32(*count)},
	}
	if err := client.Unmap(ranges); err != nil {
		return err
	}
	slog.Info("Unmap 成功")
	return nil
}

// -----------------------------------------------------------------------
// rw 命令（写后读验证）
// -----------------------------------------------------------------------

func runRW(client *tcp.Client, args []string) error {
	fs := flag.NewFlagSet("rw", flag.ExitOnError)
	lba := fs.Uint64("lba", 0, "起始 LBA")
	count := fs.Uint("count", 8, "块数")
	fill := fs.Uint("fill", 0x5A, "填充字节值（0-255）")
	fs.Parse(args)

	totalBytes := uint64(*count) * uint64(client.BlockSize())
	fillByte := byte(*fill & 0xFF)

	// 写入
	writeData := make([]byte, totalBytes)
	for i := range writeData {
		writeData[i] = fillByte
	}
	slog.Info("rw: 写入", "lba", *lba, "count", *count, "total_bytes", totalBytes, "fill", fmt.Sprintf("0x%02X", fillByte))
	if err := client.Write(*lba, writeData); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	slog.Info("rw: 写入成功")

	// 读取
	slog.Info("rw: 读取", "lba", *lba, "count", *count)
	readData, err := client.Read(*lba, uint32(*count))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	slog.Info("rw: 读取成功")
	printHexSummary(readData)

	// 验证
	mismatch := 0
	for i, b := range readData {
		if b != fillByte {
			if mismatch < 10 {
				slog.Error("数据不匹配", "offset", i, "expected", fmt.Sprintf("0x%02X", fillByte), "got", fmt.Sprintf("0x%02X", b))
			}
			mismatch++
		}
	}
	if mismatch > 0 {
		return fmt.Errorf("数据验证失败：共 %d 字节不匹配（总 %d 字节）", mismatch, totalBytes)
	}
	slog.Info("数据验证通过", "total_bytes", totalBytes, "fill", fmt.Sprintf("0x%02X", fillByte))
	return nil
}

// -----------------------------------------------------------------------
// 辅助函数
// -----------------------------------------------------------------------

// printHexSummary 打印数据的十六进制摘要（前 64 字节 + 后 16 字节）
func printHexSummary(data []byte) {
	total := len(data)
	slog.Info("数据长度", "bytes", total)

	// 打印前 64 字节
	headLen := 64
	if total < headLen {
		headLen = total
	}
	slog.Info(fmt.Sprintf("前 %d 字节", headLen))
	fmt.Printf("%s\n", formatHex(data[:headLen]))

	// 如果数据超过 64 字节，打印最后 16 字节
	if total > 64 {
		tailLen := 16
		if total-64 < tailLen {
			tailLen = total - 64
		}
		slog.Info(fmt.Sprintf("后 %d 字节", tailLen), "offset", total-tailLen)
		fmt.Printf("%s\n", formatHex(data[total-tailLen:]))
	}
}

// formatHex 将字节切片格式化为带偏移量的十六进制字符串
func formatHex(data []byte) string {
	if len(data) == 0 {
		return "(空)"
	}
	var sb strings.Builder
	for i := 0; i < len(data); i += 16 {
		end := i + 16
		if end > len(data) {
			end = len(data)
		}
		sb.WriteString(fmt.Sprintf("  %04x: %s\n", i, hex.EncodeToString(data[i:end])))
	}
	return sb.String()
}
