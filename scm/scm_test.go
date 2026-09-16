package scm_test

import (
	"math"
	"testing"

	"github.com/bemasher/rtlamr/crc"
	"github.com/bemasher/rtlamr/protocol"
	"github.com/bemasher/rtlamr/scm"
)

// Build the 96 bits of an SCM packet with a valid BCH checksum.
func scmBits(id uint32, ertType uint8, consumption uint32) string {
	bits := func(v uint64, n int) string {
		s := ""
		for i := n - 1; i >= 0; i-- {
			s += string('0' + byte(v>>uint(i)&1))
		}
		return s
	}

	// Preamble, ID[25:24], reserved, tamper phy, type, tamper enc, consumption, ID[23:0].
	s := "111110010101001100000" +
		bits(uint64(id>>24), 2) + bits(0, 1) + bits(0, 2) + bits(uint64(ertType), 4) + bits(0, 2) +
		bits(uint64(consumption), 24) + bits(uint64(id&0xFFFFFF), 24)

	// Checksum covers bytes 2..10, i.e. bits 16..80.
	var payload []byte
	for i := 16; i < 80; i += 8 {
		var b byte
		for _, c := range s[i : i+8] {
			b = b<<1 | byte(c-'0')
		}
		payload = append(payload, b)
	}
	sum := crc.NewCRC("BCH", 0, 0x6F63, 0).Checksum(payload)
	return s + bits(uint64(sum), 16)
}

// Synthesize an on-off keyed SCM burst at a known offset from the tuned
// center, run it through the decoder, and check that the message decodes
// and that its carrier offset is reported correctly.
func TestDecodeReportsCarrierOffset(t *testing.T) {
	const (
		chipLength = 72
		id         = 23456789
		ertType    = 7
		consump    = 424242
		offsetHz   = 312500.0
	)

	d := protocol.NewDecoder()
	d.RegisterProtocol(scm.NewParser(chipLength))
	d.Allocate()
	cfg := d.Cfg

	// Verify the packet is self-consistent before going through the radio path.
	packet := scmBits(id, ertType, consump)
	var pktBytes []byte
	for i := 0; i < len(packet); i += 8 {
		var b byte
		for _, c := range packet[i : i+8] {
			b = b<<1 | byte(c-'0')
		}
		pktBytes = append(pktBytes, b)
	}
	if got := scm.NewSCM(protocol.NewData(pktBytes)); got.ID != id || got.Type != ertType || got.Consumption != consump {
		t.Fatalf("packet builder produced %+v", got)
	}

	// Lay the burst down in the middle of a stream of noise.
	blocks := 8
	total := blocks * cfg.BlockSize
	start := 3*cfg.BlockSize + 517
	on := make([]bool, total)
	for i, c := range packet {
		// Manchester: a 1 is chip on then off, a 0 is off then on.
		base := start + i*cfg.SymbolLength
		for k := 0; k < cfg.ChipLength; k++ {
			on[base+k] = c == '1'
			on[base+cfg.ChipLength+k] = c == '0'
		}
	}

	seed := uint32(7)
	noise := func() float64 {
		seed = seed*1664525 + 1013904223
		return (float64(seed>>8)/float64(1<<24) - 0.5) * 16
	}
	iq := make([]byte, total*2)
	for k := 0; k < total; k++ {
		i, q := noise(), noise()
		if on[k] {
			ph := 2 * math.Pi * offsetHz * float64(k) / float64(cfg.SampleRate)
			i += 40 * math.Cos(ph)
			q += 40 * math.Sin(ph)
		}
		iq[k<<1] = byte(math.Round(127.5 + i))
		iq[k<<1|1] = byte(math.Round(127.5 + q))
	}

	var msgs []protocol.Message
	for b := 0; b < blocks; b++ {
		for msg := range d.Decode(iq[b*cfg.BlockSize2 : (b+1)*cfg.BlockSize2]) {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) == 0 {
		t.Fatal("burst did not decode")
	}
	for _, msg := range msgs {
		if msg.MeterID() != id {
			t.Fatalf("decoded id %d, want %d", msg.MeterID(), id)
		}
		off := msg.(scm.SCM).CarrierOffset
		if off == nil {
			t.Fatal("no carrier offset estimate")
		}
		if math.Abs(float64(*off)-offsetHz) > 500 {
			t.Fatalf("carrier offset %d Hz, want %.0f Hz", *off, offsetHz)
		}
	}
}
