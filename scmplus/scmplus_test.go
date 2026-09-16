package scmplus

import (
	"testing"

	"github.com/bemasher/rtlamr/protocol"
)

func TestNewSCMFields(t *testing.T) {
	data := protocol.NewData([]byte{
		0x16, 0xA3, // FrameSync
		0x1E,                   // ProtocolID
		0x9C,                   // EndpointType
		0x01, 0x23, 0x45, 0x67, // EndpointID
		0x00, 0x0A, 0xBC, 0xDE, // Consumption
		0x12, 0x34, // Tamper
		0xBE, 0xEF, // PacketCRC
	})
	got := NewSCM(data)
	want := SCM{
		FrameSync: 0x16A3, ProtocolID: 0x1E, EndpointType: 0x9C,
		EndpointID: 0x01234567, Consumption: 0x000ABCDE, Tamper: 0x1234, PacketCRC: 0xBEEF,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
