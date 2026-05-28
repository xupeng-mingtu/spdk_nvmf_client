package tcp

import (
	"encoding/binary"
)

// NVMe-oF TCP PDU 类型常量
const (
	pduTypeICReq       = 0x00 // Initialize Connection Request
	pduTypeICResp      = 0x01 // Initialize Connection Response
	pduTypeH2CTermReq  = 0x02 // Terminate Connection Request
	pduTypeC2HTermReq  = 0x03 // Terminate Connection Response
	pduTypeCapsuleCmd  = 0x04 // Command Capsule
	pduTypeCapsuleResp = 0x05 // Response Capsule
	pduTypeH2CData     = 0x06 // Host To Controller Data
	pduTypeC2HData     = 0x07 // Controller To Host Data
	pduTypeR2T         = 0x09 // Ready to Transfer
)

const (
	pduFlagHDGST = 0x01 // Header Digest
	pduFlagDDGST = 0x02 // Data Digest
)

const nvmfTCPVersion = 0x00
const defaultMaxR2T = 1

// NVMe 命令操作码
const (
	nvmeOpcFabric      = 0x7F
	nvmeOpcWrite       = 0x01
	nvmeOpcRead        = 0x02
	nvmeOpcWriteZeroes = 0x08
	nvmeOpcDatasetMgmt = 0x09 // Dataset Management (unmap)
)

// Fabric 命令子类型
const (
	nvmfFabricCmdConnect     = 0x01
	nvmfFabricCmdPropertySet = 0x00
	nvmfFabricCmdPropertyGet = 0x04
)

const nvmeRegCC = 0x14 // Controller Configuration register offset

// SGL 描述符类型
const (
	sglTypeDataBlock      = 0x00
	sglTypeLastSegment    = 0x03
	sglTypeKeyedDataBlock = 0x04
	sglTypeTransportData  = 0x05
)

// SGL 描述符子类型
const (
	sglSubtypeAddress   = 0x00
	sglSubtypeOffset    = 0x01
	sglSubtypeTransport = 0x0A
)

const (
	psdtSGLMptrContig = 0x01
)

const (
	dsmAttrDeallocate = 0x04
)

// commonHeader: PDU 公共头部（8 字节）
// byte[0]=pdu_type, [1]=flags, [2]=hlen, [3]=pdo, [4:8]=plen
type commonHeader struct {
	PDUType uint8
	Flags   uint8
	HLen    uint8
	PDO     uint8
	PLen    uint32
}

func (h *commonHeader) encode(buf []byte) {
	buf[0] = h.PDUType
	buf[1] = h.Flags
	buf[2] = h.HLen
	buf[3] = h.PDO
	binary.LittleEndian.PutUint32(buf[4:8], h.PLen)
}

func (h *commonHeader) decode(buf []byte) {
	h.PDUType = buf[0]
	h.Flags = buf[1]
	h.HLen = buf[2]
	h.PDO = buf[3]
	h.PLen = binary.LittleEndian.Uint32(buf[4:8])
}

const icReqSize = 128

// icReqPDU: Initialize Connection Request（128 字节）
// byte[0:8]=common header, [8:10]=pfv, [10]=hpda, [11]=dgst, [12:16]=maxr2t
type icReqPDU struct {
	PDUType uint8
	Flags   uint8
	HLEN    uint8
	PDO     uint8
	PLEN    uint32
	PFV     uint16
	HPDA    uint8
	DGST    uint8
	MaxR2T  uint32
	Rsvd    [112]byte
}

func newICReqPDU() []byte {
	buf := make([]byte, icReqSize)
	buf[0] = pduTypeICReq
	buf[1] = 0x00
	buf[2] = icReqSize
	buf[3] = 0x00
	binary.LittleEndian.PutUint32(buf[4:8], icReqSize)
	binary.LittleEndian.PutUint16(buf[8:10], 0)
	buf[10] = 0x00
	buf[11] = 0x00
	binary.LittleEndian.PutUint32(buf[12:16], defaultMaxR2T-1)
	return buf
}

const icRespSize = 128

// parseICResp: 解析 ICResp PDU，返回 (maxH2CDataSize, maxR2T)
// byte[12:16]=maxh2cdata
func parseICResp(buf []byte) (maxH2CDataSize uint32, maxR2T uint32) {
	if len(buf) >= 16 {
		maxH2CDataSize = binary.LittleEndian.Uint32(buf[12:16])
	}
	return
}

// sglDescriptor: SGL 描述符（16 字节）
type sglDescriptor struct {
	Address uint64
	Length  uint32
	Rsvd    [3]byte
	Subtype uint8 // bits[3:0]=subtype, bits[7:4]=type
}

func (s *sglDescriptor) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], s.Address)
	binary.LittleEndian.PutUint32(buf[8:12], s.Length)
	buf[12] = s.Rsvd[0]
	buf[13] = s.Rsvd[1]
	buf[14] = s.Rsvd[2]
	buf[15] = s.Subtype
}

func newTransportSGL(length uint32) sglDescriptor {
	return sglDescriptor{
		Address: 0,
		Length:  length,
		Subtype: (sglTypeTransportData << 4) | sglSubtypeTransport,
	}
}

func newOffsetSGL(length uint32) sglDescriptor {
	return sglDescriptor{
		Address: 0,
		Length:  length,
		Subtype: (sglTypeDataBlock << 4) | sglSubtypeOffset,
	}
}

// nvmeCmd: NVMe 命令（64 字节）
type nvmeCmd struct {
	OPC   uint8
	FUSE  uint8
	CID   uint16
	NSID  uint32
	Rsvd2 uint32
	Rsvd3 uint32
	MPTR  uint64
	SGL1  [16]byte
	CDW10 uint32
	CDW11 uint32
	CDW12 uint32
	CDW13 uint32
	CDW14 uint32
	CDW15 uint32
}

func (c *nvmeCmd) encode(buf []byte) {
	buf[0] = c.OPC
	buf[1] = c.FUSE
	binary.LittleEndian.PutUint16(buf[2:4], c.CID)
	binary.LittleEndian.PutUint32(buf[4:8], c.NSID)
	binary.LittleEndian.PutUint32(buf[8:12], c.Rsvd2)
	binary.LittleEndian.PutUint32(buf[12:16], c.Rsvd3)
	binary.LittleEndian.PutUint64(buf[16:24], c.MPTR)
	copy(buf[24:40], c.SGL1[:])
	binary.LittleEndian.PutUint32(buf[40:44], c.CDW10)
	binary.LittleEndian.PutUint32(buf[44:48], c.CDW11)
	binary.LittleEndian.PutUint32(buf[48:52], c.CDW12)
	binary.LittleEndian.PutUint32(buf[52:56], c.CDW13)
	binary.LittleEndian.PutUint32(buf[56:60], c.CDW14)
	binary.LittleEndian.PutUint32(buf[60:64], c.CDW15)
}

func (c *nvmeCmd) setSGL1(sgl sglDescriptor) {
	c.FUSE = (c.FUSE & 0x3F) | (psdtSGLMptrContig << 6)
	sgl.encode(c.SGL1[:])
}

// nvmeCpl: NVMe 完成队列条目（16 字节）
type nvmeCpl struct {
	CDW0   uint32
	CDW1   uint32
	SQHD   uint16
	SQID   uint16
	CID    uint16
	Status uint16 // bits[0]=phase, bits[8:1]=SC, bits[11:9]=SCT
}

func (c *nvmeCpl) decode(buf []byte) {
	c.CDW0 = binary.LittleEndian.Uint32(buf[0:4])
	c.CDW1 = binary.LittleEndian.Uint32(buf[4:8])
	c.SQHD = binary.LittleEndian.Uint16(buf[8:10])
	c.SQID = binary.LittleEndian.Uint16(buf[10:12])
	c.CID = binary.LittleEndian.Uint16(buf[12:14])
	c.Status = binary.LittleEndian.Uint16(buf[14:16])
}

func (c *nvmeCpl) SC() uint8 {
	return uint8((c.Status >> 1) & 0xFF)
}

func (c *nvmeCpl) SCT() uint8 {
	return uint8((c.Status >> 9) & 0x07)
}

func (c *nvmeCpl) IsSuccess() bool {
	return c.SC() == 0 && c.SCT() == 0
}

const capsuleCmdHdrSize = 72 // 8 + 64
const capsuleRespSize = 24   // 8 + 16

const fabricConnectDataSize = 1024

// fabricConnectData: Fabric Connect 数据（1024 字节）
type fabricConnectData struct {
	HostID  [16]byte
	CntlID  uint16
	Rsvd    [238]byte
	SubNQN  [256]byte
	HostNQN [256]byte
	Rsvd2   [256]byte
}

func (d *fabricConnectData) encode() []byte {
	buf := make([]byte, fabricConnectDataSize)
	copy(buf[0:16], d.HostID[:])
	binary.LittleEndian.PutUint16(buf[16:18], d.CntlID)
	copy(buf[256:512], d.SubNQN[:])
	copy(buf[512:768], d.HostNQN[:])
	return buf
}

// buildFabricConnectCmd: 构建 Fabric Connect 命令（64 字节）
// NVMe-oF Fabric Connect 用于建立 queue pair 连接
// 字段布局（参考 NVMe-oF spec Figure 30）:
//
//	byte[0]    : OPC    - Opcode = 0x7F (Fabric command)
//	byte[1]    : PSDT   - bits[7:6]=0x01 (SGL mode)
//	byte[2:4]  : CID    - Command ID
//	byte[4]    : FCTYPE - Fabric Command Type = 0x01 (Connect)
//	byte[24:40]: SGL1   - Data Block with Offset, 指向 1024 字节 Connect Data
//	byte[40:42]: RECFMT - Record Format = 0
//	byte[42:44]: QID    - Queue ID (0=Admin, 1+=IO)
//	byte[44:46]: SQSIZE - Submission Queue Size (0-based, 31=32 entries)
//	byte[46]   : CATTR  - Queue Attributes
//	byte[48:52]: KATO   - Keep Alive Timeout (ms, admin queue only, 10000=10s)
//
// Connect Data (1024 bytes): HostID(16) + CntlID(2) + SubNQN(256) + HostNQN(256)
func buildFabricConnectCmd(qid uint16, sqSize uint16, dataLen uint32, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcFabric
	buf[1] = psdtSGLMptrContig << 6
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	buf[4] = nvmfFabricCmdConnect
	sgl := newOffsetSGL(dataLen)
	sgl.encode(buf[24:40])
	binary.LittleEndian.PutUint16(buf[40:42], 0)
	binary.LittleEndian.PutUint16(buf[42:44], qid)
	binary.LittleEndian.PutUint16(buf[44:46], sqSize)
	if qid == 0 {
		binary.LittleEndian.PutUint32(buf[48:52], 10000)
	}
	return buf
}

// buildPropertySetCmd: 构建 Property Set 命令（64 字节）
// NVMe-oF Property Set 用于设置 Controller 属性（如 CC 寄存器）
// 字段布局（参考 NVMe-oF spec Figure 32）:
//
//	byte[0]    : OPC    - Opcode = 0x7F (Fabric command)
//	byte[2:4]  : CID    - Command ID
//	byte[4]    : FCTYPE - Fabric Command Type = 0x00 (Property Set)
//	byte[40]   : ATTRIB - Attribute: bits[2:0]=size (0=4B), bits[7:3]=rsvd
//	byte[44:48]: OFST   - Property Offset, 寄存器偏移（如 CC=0x14）
//	byte[48:56]: VALUE  - 要设置的值（64-bit）
//
// 常见用途: 设置 CC.EN=1 启用 Controller
func buildPropertySetCmd(offset uint32, value uint64, cid uint16) []byte {
	buf := make([]byte, 64)
	buf[0] = nvmeOpcFabric
	binary.LittleEndian.PutUint16(buf[2:4], cid)
	buf[4] = nvmfFabricCmdPropertySet
	binary.LittleEndian.PutUint32(buf[44:48], offset)
	binary.LittleEndian.PutUint64(buf[48:56], value)
	return buf
}

// buildCCValue: 构建 Controller Configuration (CC) 寄存器值
// CC 寄存器用于启用 Controller 和配置基本参数（参考 NVMe spec 3.1.2）
// 字段位定义:
//
//	bit[0]   : EN     - Enable, 1=启用 Controller
//	bit[4:6] : CSS    - Command Set Selected, 0=NVM command set
//	bit[7:10]: MPS    - Memory Page Size, 0=4KB
//	bit[11:13]: AMS   - Arbitration Mechanism, 0=Round Robin
//	bit[16:19]: IOSQES- IO Submission Queue Entry Size, 6=64 bytes
//	bit[20:23]: IOCQES- IO Completion Queue Entry Size, 4=16 bytes
//
// 计算: EN=1 | IOSQES=6<<16 | IOCQES=4<<20 = 0x00460001
func buildCCValue() uint64 {
	var cc uint32
	cc |= 1 << 0
	cc |= 6 << 16
	cc |= 4 << 20
	return uint64(cc)
}

// dsmRange: Dataset Management range 描述符（16 字节）
type dsmRange struct {
	Attributes uint32
	Length     uint32
	StartLBA   uint64
}

func (d *dsmRange) encode(buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], d.Attributes)
	binary.LittleEndian.PutUint32(buf[4:8], d.Length)
	binary.LittleEndian.PutUint64(buf[8:16], d.StartLBA)
}
