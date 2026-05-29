package tcp

import (
	"encoding/binary"
	"fmt"
)

// buildReadCmd 构建 NVMe Read 命令（64 字节）
// 字段布局（NVMe Command Format）:
//
//	byte[0]    : OPC   - Opcode = 0x02 (Read)
//	byte[1]    : PSDT  - PRP/SGL Selection, bits[7:6]=0x01 (SGL, MPTR contiguous)
//	byte[2:4]  : CID   - Command ID, 唯一标识此命令
//	byte[4:8]  : NSID  - Namespace ID, 命名空间标识符
//	byte[24:40]: SGL1  - SGL Descriptor #1, Transport SGL (通过 C2HData PDU 接收数据)
//	byte[40:48]: CDW10-11, SLBA - Starting LBA (64-bit), 起始逻辑块地址
//	byte[48:52]: CDW12, NLB - Number of Blocks (0-based), 块数减1
func buildReadCmd(lba uint64, lbaCount uint32, nsid uint32, totalBytes uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcRead
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := newTransportSGL(totalBytes)
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint64(buf[40:48], lba)
	binary.LittleEndian.PutUint32(buf[48:52], lbaCount-1)
	return buf
}

// buildWriteCmd 构建 NVMe Write 命令（64 字节）- 用于 R2T/H2CData 方式
// 字段布局:
//
//	byte[0]    : OPC   - Opcode = 0x01 (Write)
//	byte[1]    : PSDT  - bits[7:6]=0x01 (SGL mode)
//	byte[2:4]  : CID   - Command ID
//	byte[4:8]  : NSID  - Namespace ID
//	byte[24:40]: SGL1  - Transport SGL, 指示 Controller 通过 R2T 请求数据
//	byte[40:48]: SLBA  - Starting LBA, 起始逻辑块地址
//	byte[48:52]: NLB   - Number of Blocks (0-based)
//
// SGL1 使用 Transport Data Block 类型，告诉 Controller 数据将通过 H2CData PDU 发送
func buildWriteCmd(lba uint64, lbaCount uint32, nsid uint32, totalBytes uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcWrite
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := newTransportSGL(totalBytes)
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint64(buf[40:48], lba)
	binary.LittleEndian.PutUint32(buf[48:52], lbaCount-1)
	return buf
}

// buildWriteCmdWithOffsetSGL 构建使用 Offset SGL 的 Write 命令（用于 in-capsule 数据）
// 字段布局差异（相比 buildWriteCmd）:
//
//	byte[24:40]: SGL1  - Data Block with Offset SGL
//	  Address  : 数据在 PDU 中的偏移量（通常为 0，表示紧跟在 72 字节头部后）
//	  Length   : 数据长度（字节）
//	  Subtype  : 0x01 (Data Block + Offset), 表示数据在 Capsule 内部
//
// 使用场景: 小数据量（<=4KB）直接通过 Capsule Command PDU 发送，无需 R2T/H2CData 流程
func buildWriteCmdWithOffsetSGL(lba uint64, lbaCount uint32, nsid uint32, offset uint32, length uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcWrite
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := sglDescriptor{
		Address: uint64(offset),
		Length:  length,
		Subtype: (sglTypeDataBlock << 4) | sglSubtypeOffset,
	}
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint64(buf[40:48], lba)
	binary.LittleEndian.PutUint32(buf[48:52], lbaCount-1)
	return buf
}

// buildWriteZeroesCmd 构建 NVMe Write Zeroes 命令（64 字节）
// 字段布局:
//
//	byte[0]    : OPC   - Opcode = 0x08 (Write Zeroes)
//	byte[24:40]: SGL1  - Keyed Data Block, Length=0
//	  不传输数据，Controller 直接将指定 LBA 范围写零
//	byte[40:48]: SLBA  - Starting LBA
//	byte[48:52]: NLB   - Number of Blocks (0-based)
func buildWriteZeroesCmd(lba uint64, lbaCount uint32, nsid uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcWriteZeroes
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := sglDescriptor{
		Address: 0,
		Length:  0,
		Subtype: (sglTypeKeyedDataBlock << 4) | sglSubtypeAddress,
	}
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint64(buf[40:48], lba)
	binary.LittleEndian.PutUint32(buf[48:52], lbaCount-1)
	return buf
}

// buildDatasetMgmtCmd 构建 NVMe Dataset Management 命令（64 字节）- R2T/H2CData 方式
// 字段布局:
//
//	byte[0]    : OPC   - Opcode = 0x09 (Dataset Management)
//	byte[24:40]: SGL1  - Transport SGL, 数据通过 H2CData PDU 发送
//	byte[40:44]: CDW10, NR - Number of Ranges (0-based), DSM range 数量减1
//	byte[44:48]: CDW11, AD - Attribute Deallocate bit=1, 执行 Deallocate (Unmap) 操作
//
// 数据格式: 每个 DSM range 16 字节（Attributes 4B + Length 4B + StartLBA 8B）
func buildDatasetMgmtCmd(nsid uint32, numRanges uint32, totalBytes uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcDatasetMgmt
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := newTransportSGL(totalBytes)
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint32(buf[40:44], numRanges-1)
	binary.LittleEndian.PutUint32(buf[44:48], dsmAttrDeallocate)
	return buf
}

// buildDatasetMgmtCmdWithInCapsule 构建 Dataset Management 命令（in-capsule 方式）
// 字段布局差异（相比 buildDatasetMgmtCmd）:
//
//	byte[24:40]: SGL1  - Data Block with Offset SGL
//	  Address  : 数据在 PDU 中的偏移量
//	  Length   : DSM 数据总长度（numRanges * 16 bytes）
//	  Subtype  : 0x01 (Data Block + Offset)
//
// 使用场景: 小数据量（<=4KB，即最多 256 个 ranges）直接通过 Capsule 发送
func buildDatasetMgmtCmdWithInCapsule(nsid uint32, numRanges uint32, offset uint32, length uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcDatasetMgmt
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	binary.LittleEndian.PutUint32(buf[4:8], nsid)
	sgl := sglDescriptor{
		Address: uint64(offset),
		Length:  length,
		Subtype: (sglTypeDataBlock << 4) | sglSubtypeOffset,
	}
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint32(buf[40:44], numRanges-1)
	binary.LittleEndian.PutUint32(buf[44:48], dsmAttrDeallocate)
	return buf
}

func (qp *qpair) Read(lba uint64, lbaCount uint32, nsid uint32, blockSize uint32) ([]byte, error) {
	totalBytes := lbaCount * blockSize
	cid := qp.allocCID()
	cmdBuf := buildReadCmd(lba, lbaCount, nsid, totalBytes, cid)

	readBuf := make([]byte, totalBytes)
	cpl, err := qp.sendIOCmd(cmdBuf, nil, readBuf)
	if err != nil {
		return nil, fmt.Errorf("read(lba=%d, count=%d): %w", lba, lbaCount, err)
	}
	if !cpl.IsSuccess() {
		return nil, fmt.Errorf("read(lba=%d, count=%d) failed: SC=%d SCT=%d", lba, lbaCount, cpl.SC(), cpl.SCT())
	}
	return readBuf, nil
}

func (qp *qpair) Write(lba uint64, nsid uint32, blockSize uint32, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("write: data is empty")
	}
	if uint32(len(data))%blockSize != 0 {
		return fmt.Errorf("write: data length %d is not a multiple of block size %d", len(data), blockSize)
	}
	lbaCount := uint32(len(data)) / blockSize
	cid := qp.allocCID()

	var cmdBuf []byte
	if qp.maxCapsuleDataSize > 0 && uint32(len(data)) <= qp.maxCapsuleDataSize {
		cmdBuf = buildWriteCmdWithOffsetSGL(lba, lbaCount, nsid, 0, uint32(len(data)), cid)
	} else {
		cmdBuf = buildWriteCmd(lba, lbaCount, nsid, uint32(len(data)), cid)
	}

	cpl, err := qp.sendIOCmd(cmdBuf, data, nil)
	if err != nil {
		return fmt.Errorf("write(lba=%d, count=%d): %w", lba, lbaCount, err)
	}
	if !cpl.IsSuccess() {
		return fmt.Errorf("write(lba=%d, count=%d) failed: SC=%d SCT=%d", lba, lbaCount, cpl.SC(), cpl.SCT())
	}
	return nil
}

func (qp *qpair) WriteZeroes(lba uint64, lbaCount uint32, nsid uint32) error {
	cid := qp.allocCID()
	cmdBuf := buildWriteZeroesCmd(lba, lbaCount, nsid, cid)

	cpl, err := qp.sendIOCmd(cmdBuf, nil, nil)
	if err != nil {
		return fmt.Errorf("write_zeroes(lba=%d, count=%d): %w", lba, lbaCount, err)
	}
	if !cpl.IsSuccess() {
		return fmt.Errorf("write_zeroes(lba=%d, count=%d) failed: SC=%d SCT=%d", lba, lbaCount, cpl.SC(), cpl.SCT())
	}
	return nil
}

type UnmapRange struct {
	StartLBA uint64
	LBACount uint32
}

func (qp *qpair) Unmap(nsid uint32, ranges []UnmapRange) error {
	if len(ranges) == 0 {
		return fmt.Errorf("unmap: no ranges specified")
	}
	if len(ranges) > 256 {
		return fmt.Errorf("unmap: too many ranges (%d > 256)", len(ranges))
	}

	const dsmRangeSize = 16
	totalBytes := uint32(len(ranges) * dsmRangeSize)
	dsmData := make([]byte, totalBytes)
	for i, r := range ranges {
		dr := dsmRange{
			Attributes: 0,
			Length:     r.LBACount,
			StartLBA:   r.StartLBA,
		}
		dr.encode(dsmData[i*dsmRangeSize:])
	}

	cid := qp.allocCID()

	var cmdBuf []byte
	if qp.maxCapsuleDataSize > 0 && totalBytes <= qp.maxCapsuleDataSize {
		cmdBuf = buildDatasetMgmtCmdWithInCapsule(nsid, uint32(len(ranges)), 0, totalBytes, cid)
	} else {
		cmdBuf = buildDatasetMgmtCmd(nsid, uint32(len(ranges)), totalBytes, cid)
	}

	cpl, err := qp.sendIOCmd(cmdBuf, dsmData, nil)
	if err != nil {
		return fmt.Errorf("unmap(%d ranges): %w", len(ranges), err)
	}
	if !cpl.IsSuccess() {
		return fmt.Errorf("unmap(%d ranges) failed: SC=%d SCT=%d", len(ranges), cpl.SC(), cpl.SCT())
	}
	return nil
}
