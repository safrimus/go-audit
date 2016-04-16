package main

import (
	"syscall"
	"sync/atomic"
	"bytes"
	"encoding/binary"
	"time"
	"errors"
)

var Endianness = binary.LittleEndian

const (
	//http://lxr.free-electrons.com/source/include/uapi/linux/audit.h#L398
	MAX_AUDIT_MESSAGE_LENGTH = 8970
)

//TODO: this should live in a marshaller
type AuditStatusPayload struct {
	Mask            uint32
	Enabled         uint32
	Failure         uint32
	Pid             uint32
	RateLimit       uint32
	BacklogLimit    uint32
	Lost            uint32
	Backlog         uint32
	Version         uint32
	BacklogWaitTime uint32
}

//An alias to give the header a similar name here
type NetlinkPacket syscall.NlMsghdr

type NetlinkClient struct {
	fd             int
	address        syscall.Sockaddr
	seq            uint32
	buf            []byte
}

func NewNetlinkClient(recvSize int) (*NetlinkClient) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, syscall.NETLINK_AUDIT)
	if err != nil {
		el.Fatalln("Could not create a socket:", err)
	}

	n := &NetlinkClient{
		fd: fd,
		address: &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 0, Pid: 0},
		buf:     make([]byte, MAX_AUDIT_MESSAGE_LENGTH),
	}

	if err = syscall.Bind(fd, n.address); err != nil {
		syscall.Close(fd)
		el.Fatalln("Could not bind to netlink socket:", err)
	}

	// Set the buffer size if we were asked
	if (recvSize > 0) {
		err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, recvSize)
	}

	// Print the current receive buffer size
	if v, err := syscall.GetsockoptInt(n.fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF); err == nil {
		l.Println("Socket receive buffer size:", v)
	}

	go func() {
		for {
			n.KeepConnection()
			time.Sleep(time.Second * 5)
		}
	}()

	return n
}

func (n *NetlinkClient) Send(np *NetlinkPacket, a *AuditStatusPayload) error {
	//We need to get the length first. This is a bit wasteful, but requests are rare so yolo..
	buf := new(bytes.Buffer)
	var length int

	np.Seq = atomic.AddUint32(&n.seq, 1)

	for {
		buf.Reset()
		binary.Write(buf, Endianness, np)
		binary.Write(buf, Endianness, a)
		if np.Len == 0 {
			length = len(buf.Bytes())
			np.Len = uint32(length)
		} else {
			break
		}
	}

	if err := syscall.Sendto(n.fd, buf.Bytes(), 0, n.address); err != nil {
		return err
	}

	return nil
}

func (n *NetlinkClient) Receive() (*syscall.NetlinkMessage, error) {
	nlen, _, err := syscall.Recvfrom(n.fd, n.buf, 0)
	if err != nil {
		return nil, err
	}

	if nlen < 1 {
		return nil, errors.New("Got a 0 length packet")
	}

	msg := &syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{
			Len: Endianness.Uint32(n.buf[0:4]),
			Type: Endianness.Uint16(n.buf[4:6]),
			Flags: Endianness.Uint16(n.buf[6:8]),
			Seq: Endianness.Uint32(n.buf[8:12]),
			Pid: Endianness.Uint32(n.buf[12:16]),
		},
		Data: n.buf[syscall.SizeofNlMsghdr:nlen],
	}

	return msg, nil
}

func (n *NetlinkClient) KeepConnection() {
	payload := &AuditStatusPayload{
		Mask: 4,
		Enabled: 1,
		Pid: uint32(syscall.Getpid()),
		//TODO: Failure: http://lxr.free-electrons.com/source/include/uapi/linux/audit.h#L338
	}

	packet := &NetlinkPacket{
		Type: uint16(1001),
		Flags: syscall.NLM_F_REQUEST | syscall.NLM_F_ACK,
		Pid: uint32(syscall.Getpid()),
	}

	err := n.Send(packet, payload)
	if err != nil {
		el.Println("Error occurred while trying to keep the connection:", err)
	}
}
