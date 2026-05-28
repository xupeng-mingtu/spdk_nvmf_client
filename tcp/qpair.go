package tcp

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"
)

type qpairType int

const (
	qpairAdmin qpairType = iota
	qpairIO
)

type qpair struct {
	qid     uint16
	ctrlID  uint16
	conn    *tcpConn
	nextCID uint16
}

func newAdminQpair() *qpair {
	return &qpair{
		qid:    0,
		ctrlID: 0xFFFF,
	}
}

func newIOQpair(ctrlID uint16) *qpair {
	return &qpair{
		qid:    1,
		ctrlID: ctrlID,
	}
}

func (qp *qpair) allocCID() uint16 {
	cid := qp.nextCID
	qp.nextCID++
	return cid
}

func (qp *qpair) name() string {
	if qp.qid == 0 {
		return "AdminQpair"
	}
	return "IoQpair"
}

func (qp *qpair) connect(addr, hostnqn, subnqn string) error {
	slog.Info(qp.name()+" connecting", "addr", addr)
	conn, err := dialTCP(addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("qpair[%d] dial: %w", qp.qid, err)
	}
	qp.conn = conn
	slog.Info(qp.name() + " TCP connected")

	slog.Debug(qp.name() + " sending ICReq")
	if err := conn.sendICReq(); err != nil {
		return fmt.Errorf("qpair[%d] send ICReq: %w", qp.qid, err)
	}
	slog.Debug(qp.name() + " waiting ICResp")
	_, _, err = conn.recvICResp()
	if err != nil {
		return fmt.Errorf("qpair[%d] recv ICResp: %w", qp.qid, err)
	}
	slog.Info(qp.name() + " ICResp received")

	slog.Info(qp.name()+" sending Fabric Connect", "addr", addr)
	if err := qp.sendFabricConnect(hostnqn, subnqn); err != nil {
		return fmt.Errorf("qpair[%d] fabric connect: %w", qp.qid, err)
	}
	slog.Info(qp.name() + " Fabric Connect done")

	return nil
}

func (qp *qpair) sendFabricConnect(hostnqn, subnqn string) error {
	cid := qp.allocCID()

	connData := &fabricConnectData{
		CntlID: qp.ctrlID,
	}
	copy(connData.HostNQN[:], []byte(hostnqn))
	copy(connData.SubNQN[:], []byte(subnqn))
	copy(connData.HostID[:], []byte{0x43, 0xc4, 0x86, 0x37, 0xf5, 0x50, 0x4f, 0x06,
		0x8d, 0x28, 0x38, 0x70, 0xc9, 0x60, 0x4d, 0xd2})

	dataBytes := connData.encode()
	cmdBuf := buildFabricConnectCmd(qp.qid, 31, uint32(len(dataBytes)), cid)

	slog.Debug(qp.name()+" sendFabricConnect: sending capsule cmd", "cid", cid, "data_bytes", len(dataBytes))
	if err := qp.conn.sendCapsuleCmd(cmdBuf, dataBytes); err != nil {
		return fmt.Errorf("send connect capsule: %w", err)
	}
	slog.Debug(qp.name() + " sendFabricConnect: capsule sent, waiting response")

	cpl, err := qp.conn.recvResponse(cid, nil, nil)
	if err != nil {
		return fmt.Errorf("recv connect response: %w", err)
	}
	slog.Info(qp.name()+" sendFabricConnect: response received", "cdw0", cpl.CDW0, "sc", cpl.SC(), "sct", cpl.SCT())

	if !cpl.IsSuccess() {
		return fmt.Errorf("fabric connect failed: SC=%d SCT=%d", cpl.SC(), cpl.SCT())
	}

	newCtrlID := uint16(cpl.CDW0 & 0xFFFF)
	if qp.qid == 0 {
		qp.ctrlID = newCtrlID
		slog.Info(qp.name()+" ctrlID updated", "ctrl_id", newCtrlID)
	}

	return nil
}

func (qp *qpair) sendPropertySet(offset uint32, value uint64) error {
	cid := qp.allocCID()
	cmdBuf := buildPropertySetCmd(offset, value, cid)

	slog.Debug(qp.name()+" sendPropertySet", "offset", offset, "value", value, "cid", cid)
	if err := qp.conn.sendCapsuleCmd(cmdBuf, nil); err != nil {
		return fmt.Errorf("send property set capsule: %w", err)
	}
	slog.Debug(qp.name() + " sendPropertySet: sent, waiting response")

	cpl, err := qp.conn.recvResponse(cid, nil, nil)
	if err != nil {
		return fmt.Errorf("recv property set response: %w", err)
	}
	slog.Info(qp.name()+" sendPropertySet: response received", "sc", cpl.SC(), "sct", cpl.SCT())

	if !cpl.IsSuccess() {
		return fmt.Errorf("property set failed: SC=%d SCT=%d", cpl.SC(), cpl.SCT())
	}

	return nil
}

func (qp *qpair) enableController() error {
	ccValue := buildCCValue()
	return qp.sendPropertySet(nvmeRegCC, ccValue)
}

func (qp *qpair) close() {
	if qp.conn != nil {
		qp.conn.close()
		qp.conn = nil
	}
}

func (qp *qpair) sendIOCmd(cmdBuf []byte, writeBuf []byte, readBuf []byte) (*nvmeCpl, error) {
	cid := binary.LittleEndian.Uint16(cmdBuf[2:4])
	opcode := cmdBuf[0]
	slog.Debug(qp.name()+" sendIOCmd", "opcode", opcode, "cid", cid, "has_write_buf", writeBuf != nil, "has_read_buf", readBuf != nil)

	if writeBuf != nil && len(writeBuf) <= 4096 {
		slog.Debug(qp.name()+" sendIOCmd: using in-capsule data", "data_len", len(writeBuf))
		if err := qp.conn.sendCapsuleCmd(cmdBuf, writeBuf); err != nil {
			return nil, fmt.Errorf("send IO capsule with data: %w", err)
		}
	} else {
		slog.Debug(qp.name() + " sendIOCmd: sending capsule cmd (no in-capsule data)")
		if err := qp.conn.sendCapsuleCmd(cmdBuf, nil); err != nil {
			return nil, fmt.Errorf("send IO capsule: %w", err)
		}
	}
	slog.Debug(qp.name() + " sendIOCmd: waiting response")

	cpl, err := qp.conn.recvResponse(cid, writeBuf, readBuf)
	if err != nil {
		return nil, fmt.Errorf("recv IO response: %w", err)
	}
	slog.Debug(qp.name()+" sendIOCmd: response received", "sc", cpl.SC(), "sct", cpl.SCT())

	return cpl, nil
}

func (qp *qpair) sendIOCmdWithInCapsuleData(cmdBuf []byte, inCapsuleData []byte) (*nvmeCpl, error) {
	cid := binary.LittleEndian.Uint16(cmdBuf[2:4])

	if err := qp.conn.sendCapsuleCmd(cmdBuf, inCapsuleData); err != nil {
		return nil, fmt.Errorf("send IO capsule with data: %w", err)
	}

	cpl, err := qp.conn.recvResponse(cid, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("recv IO response: %w", err)
	}

	return cpl, nil
}
