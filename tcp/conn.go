package tcp

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const (
	c2hDataFlagsSuccess = 1 << 3
)

type tcpConn struct {
	conn net.Conn
}

func dialTCP(addr string, timeout time.Duration) (*tcpConn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", addr, err)
	}
	return &tcpConn{conn: conn}, nil
}

func (c *tcpConn) close() error {
	return c.conn.Close()
}

func (c *tcpConn) sendRaw(data []byte) error {
	n, err := c.conn.Write(data)
	if err != nil {
		return fmt.Errorf("send pdu: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("send pdu: short write %d/%d", n, len(data))
	}
	slog.Debug("sendRaw", "bytes_sent", n)
	return nil
}

func (c *tcpConn) recvRaw(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(c.conn, buf)
	if err != nil {
		return nil, fmt.Errorf("recv %d bytes: %w", n, err)
	}
	return buf, nil
}

func (c *tcpConn) sendICReq() error {
	pdu := newICReqPDU()
	return c.sendRaw(pdu)
}

func (c *tcpConn) recvICResp() (maxH2CDataSize uint32, maxR2T uint32, err error) {
	hdr, err := c.recvRaw(8)
	if err != nil {
		return 0, 0, fmt.Errorf("recv ICResp header: %w", err)
	}
	if hdr[0] != pduTypeICResp {
		return 0, 0, fmt.Errorf("expected ICResp PDU type 0x%02x, got 0x%02x", pduTypeICResp, hdr[0])
	}
	plen := binary.LittleEndian.Uint32(hdr[4:8])
	if plen < 8 {
		return 0, 0, fmt.Errorf("ICResp PLEN too small: %d", plen)
	}
	rest, err := c.recvRaw(int(plen - 8))
	if err != nil {
		return 0, 0, fmt.Errorf("recv ICResp body: %w", err)
	}
	buf := append(hdr, rest...)
	maxH2CDataSize, maxR2T = parseICResp(buf)
	return
}

// sendCapsuleCmd 发送 Capsule Command PDU
// PDU 布局：byte[0:7]=Common Header, byte[8:71]=NVMe Command, byte[72:]=In-capsule data
func (c *tcpConn) sendCapsuleCmd(cmdBuf []byte, inCapsuleData []byte) error {
	const capsuleCmdTotalHdrSize = 72 // 8 + 64

	dataLen := uint32(0)
	if inCapsuleData != nil {
		dataLen = uint32(len(inCapsuleData))
	}
	pduLen := uint32(capsuleCmdTotalHdrSize) + dataLen

	hdr := make([]byte, 8)
	hdr[0] = pduTypeCapsuleCmd
	hdr[1] = 0x00
	hdr[2] = capsuleCmdTotalHdrSize
	if len(inCapsuleData) > 0 {
		hdr[3] = capsuleCmdTotalHdrSize
	} else {
		hdr[3] = 0x00
	}
	binary.LittleEndian.PutUint32(hdr[4:8], pduLen)

	pdu := make([]byte, 0, pduLen)
	pdu = append(pdu, hdr...)
	pdu = append(pdu, cmdBuf...)
	if inCapsuleData != nil {
		pdu = append(pdu, inCapsuleData...)
	}

	return c.sendRaw(pdu)
}

// sendH2CData 发送 H2CData PDU（响应 R2T 请求）
// H2CData specific header: cccid(2) + ttag(2) + datao(4) + datal(4) + rsvd(4)
func (c *tcpConn) sendH2CData(cccid uint16, ttag uint16, datao uint32, dataBuf []byte) error {
	dataLen := uint32(len(dataBuf))
	const h2cHdrSize = 24 // 8 + 16
	pduLen := uint32(h2cHdrSize) + dataLen

	pdu := make([]byte, h2cHdrSize)
	pdu[0] = pduTypeH2CData
	pdu[1] = 0x04 // LAST_PDU = 1
	pdu[2] = h2cHdrSize
	pdu[3] = h2cHdrSize
	binary.LittleEndian.PutUint32(pdu[4:8], pduLen)
	binary.LittleEndian.PutUint16(pdu[8:10], cccid)
	binary.LittleEndian.PutUint16(pdu[10:12], ttag)
	binary.LittleEndian.PutUint32(pdu[12:16], datao)
	binary.LittleEndian.PutUint32(pdu[16:20], dataLen)

	pdu = append(pdu, dataBuf...)
	return c.sendRaw(pdu)
}

func (c *tcpConn) recvPDUHeader() (pduType uint8, pduLen uint32, err error) {
	hdr, err := c.recvRaw(8)
	if err != nil {
		return 0, 0, fmt.Errorf("recv PDU header: %w", err)
	}
	pduType = hdr[0]
	pduLen = binary.LittleEndian.Uint32(hdr[4:8])
	return
}

func (c *tcpConn) recvPDUHeaderWithFlags() (pduType uint8, flags uint8, pduLen uint32, err error) {
	hdr, err := c.recvRaw(8)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("recv PDU header: %w", err)
	}
	pduType = hdr[0]
	flags = hdr[1]
	pduLen = binary.LittleEndian.Uint32(hdr[4:8])
	return
}

func (c *tcpConn) recvPDUBody(pduLen uint32) ([]byte, error) {
	if pduLen < 8 {
		return nil, fmt.Errorf("invalid pduLen %d", pduLen)
	}
	bodyLen := pduLen - 8
	if bodyLen == 0 {
		return []byte{}, nil
	}
	return c.recvRaw(int(bodyLen))
}

// recvResponse 接收命令响应，处理 R2T 和 C2HData PDU
func (c *tcpConn) recvResponse(cid uint16, writeBuf []byte, readBuf []byte) (*nvmeCpl, error) {
	for {
		pduType, flags, pduLen, err := c.recvPDUHeaderWithFlags()
		if err != nil {
			return nil, err
		}
		slog.Debug("recvResponse: received PDU", "pdu_type", pduType, "flags", flags, "pdu_len", pduLen)

		body, err := c.recvPDUBody(pduLen)
		if err != nil {
			return nil, err
		}
		slog.Debug("recvResponse: received body", "body_len", len(body))

		switch pduType {
		case pduTypeCapsuleResp:
			if len(body) < 16 {
				return nil, fmt.Errorf("capsule resp body too short: %d", len(body))
			}
			cpl := &nvmeCpl{}
			cpl.decode(body[:16])
			slog.Debug("recvResponse: CapsuleResp received", "sc", cpl.SC(), "sct", cpl.SCT())
			return cpl, nil

		case pduTypeR2T:
			// R2T body: cccid(2) + ttag(2) + datao(4) + datal(4) + rsvd(4)
			if len(body) < 16 {
				return nil, fmt.Errorf("R2T body too short: %d", len(body))
			}
			cccid := binary.LittleEndian.Uint16(body[0:2])
			ttag := binary.LittleEndian.Uint16(body[2:4])
			datao := binary.LittleEndian.Uint32(body[4:8])
			datal := binary.LittleEndian.Uint32(body[8:12])
			slog.Debug("recvResponse: R2T received", "cccid", cccid, "ttag", ttag, "data_offset", datao, "data_len", datal)

			if writeBuf == nil {
				return nil, fmt.Errorf("received R2T but no write buffer provided")
			}
			end := datao + datal
			if int(end) > len(writeBuf) {
				return nil, fmt.Errorf("R2T requests data [%d:%d] but write buf len=%d", datao, end, len(writeBuf))
			}
			slog.Debug("recvResponse: sending H2CData", "cccid", cccid, "ttag", ttag, "datao", datao, "datal", datal)
			if err := c.sendH2CData(cccid, ttag, datao, writeBuf[datao:end]); err != nil {
				return nil, fmt.Errorf("send H2CData: %w", err)
			}
			slog.Debug("recvResponse: H2CData sent")

		case pduTypeC2HData:
			// C2HData body: cccid(2) + ttag(2) + datao(4) + datal(4) + rsvd(4) + data
			if len(body) < 16 {
				return nil, fmt.Errorf("C2HData body too short: %d", len(body))
			}
			cccid := binary.LittleEndian.Uint16(body[0:2])
			ttag := binary.LittleEndian.Uint16(body[2:4])
			datao := binary.LittleEndian.Uint32(body[4:8])
			datal := binary.LittleEndian.Uint32(body[8:12])
			data := body[16:]
			if uint32(len(data)) < datal {
				return nil, fmt.Errorf("C2HData: expected %d bytes, got %d", datal, len(data))
			}
			if readBuf != nil {
				end := datao + datal
				if int(end) > len(readBuf) {
					return nil, fmt.Errorf("C2HData offset+len [%d:%d] exceeds read buf len=%d", datao, end, len(readBuf))
				}
				copy(readBuf[datao:end], data[:datal])
			}
			slog.Debug("recvResponse: C2HData received", "cccid", cccid, "ttag", ttag, "datao", datao, "datal", datal)
			if flags&c2hDataFlagsSuccess != 0 {
				slog.Debug("recvResponse: C2HData SUCCESS flag set")
				return &nvmeCpl{CDW0: 0, Status: 0}, nil
			}

		case pduTypeC2HTermReq:
			return nil, fmt.Errorf("controller sent TermReq PDU")

		default:
			return nil, fmt.Errorf("unexpected PDU type 0x%02x (len=%d)", pduType, pduLen)
		}
	}
}
