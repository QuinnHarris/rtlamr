package protocol

import (
	"math"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	n := 64
	re := make([]float64, n)
	im := make([]float64, n)
	for k := range re {
		re[k] = math.Sin(float64(k)*0.7) + 0.3*float64(k%5)
		im[k] = math.Cos(float64(k) * 1.3)
	}
	wantRe := make([]float64, n)
	wantIm := make([]float64, n)
	for f := 0; f < n; f++ {
		for k := 0; k < n; k++ {
			ang := -2 * math.Pi * float64(f*k) / float64(n)
			wantRe[f] += re[k]*math.Cos(ang) - im[k]*math.Sin(ang)
			wantIm[f] += re[k]*math.Sin(ang) + im[k]*math.Cos(ang)
		}
	}
	fft(re, im)
	for f := 0; f < n; f++ {
		if math.Abs(re[f]-wantRe[f]) > 1e-9 || math.Abs(im[f]-wantIm[f]) > 1e-9 {
			t.Fatalf("bin %d: got (%g,%g) want (%g,%g)", f, re[f], im[f], wantRe[f], wantIm[f])
		}
	}
}

func TestCarrierOffsetOfNoisyOOKTone(t *testing.T) {
	var d Decoder
	d.Cfg.SampleRate = 2359296
	d.Cfg.ChipLength = 72
	d.Cfg.PacketLength = 116 * 144
	d.Cfg.BlockSize = 8192
	d.Cfg.BufferLength = d.Cfg.PacketLength + d.Cfg.BlockSize
	d.IQ = make([]byte, d.Cfg.BufferLength<<1)
	mag := make([]float32, d.Cfg.BufferLength)
	// Weak OOK tone at -846 kHz, 50 % chip duty, amplitude well below noise.
	idx := 4720
	fTone := -846327.0
	seed := uint32(1)
	noise := func() float64 {
		seed = seed*1664525 + 1013904223
		return (float64(seed>>8)/float64(1<<24) - 0.5) * 60
	}
	for k := 0; k < d.Cfg.BufferLength; k++ {
		i, q := noise(), noise()
		p := k - idx
		if p >= 0 && p < d.Cfg.PacketLength && (p/d.Cfg.ChipLength)%2 == 0 {
			ph := 2 * math.Pi * fTone * float64(k) / float64(d.Cfg.SampleRate)
			i += 6 * math.Cos(ph)
			q += 6 * math.Sin(ph)
		}
		d.IQ[k<<1] = byte(math.Round(127.5 + i))
		d.IQ[k<<1|1] = byte(math.Round(127.5 + q))
		mag[k] = float32(math.Hypot(i, q))
	}
	off, ok := d.CarrierOffsetHz(idx, d.Cfg.PacketLength, mag)
	if !ok {
		t.Fatal("no estimate")
	}
	if math.Abs(off-fTone) > 500 {
		t.Fatalf("offset %.0f, want %.0f", off, fTone)
	}
	// Pure noise must not produce an estimate.
	for k := range d.IQ {
		d.IQ[k] = byte(math.Round(127.5 + noise()))
	}
	if _, ok := d.CarrierOffsetHz(idx, d.Cfg.PacketLength, mag); ok {
		t.Fatal("noise-only window produced an estimate")
	}
}
