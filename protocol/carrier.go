package protocol

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

var (
	carrierDebug = os.Getenv("RTLAMR_CARRIER_DEBUG") != ""
	// Directory to dump each packet's raw IQ window and magnitudes into.
	carrierDump = os.Getenv("RTLAMR_CARRIER_DUMP")
	dumpSeq     int
)

// Bins within this distance of the tuned center are skipped when searching
// for the carrier: the RTL-SDR's DC spike lives there.
const carrierDCExcludeHz = 4000.0

// A peak must exceed the mean bin power by this factor to be trusted. The
// largest of ~32k noise bins is about 10× the mean; decoded packets show
// well over 1000×.
const carrierMinProminence = 25.0

// PacketCarrierOffset is CarrierOffsetHz for a packet of the given number of
// symbols found at idx, rounded to whole Hz for a message's CarrierOffset
// field. It returns nil when no estimate could be made, and is safe to call
// on a nil or unallocated decoder.
func (d *Decoder) PacketCarrierOffset(idx, symbols int, mag []float32) *int64 {
	if d == nil || d.IQ == nil {
		return nil
	}
	off, ok := d.CarrierOffsetHz(idx, symbols*d.Cfg.SymbolLength, mag)
	if !ok {
		return nil
	}
	v := int64(math.Round(off))
	return &v
}

// FormatCarrierOffset renders a message's CarrierOffset for CSV output: the
// value in Hz, or empty when there is no estimate.
func FormatCarrierOffset(off *int64) string {
	if off == nil {
		return ""
	}
	return strconv.FormatInt(*off, 10)
}

// CarrierOffsetHz estimates how far the carrier of the packet occupying
// samples [idx, idx+length) of the decoder's IQ buffer sits from the
// receiver's tuned center, in Hz. mag, if not nil, must be the packet's
// magnitude samples in the same coordinates (used only for debug dumps).
//
// The protocols decoded here are on-off keyed, so the packet is a tone at
// the channel frequency switched on and off at the chip rate. Decodable
// packets are often only a few dB above the noise floor sample by sample
// (the matched filter integrates a whole chip), so the carrier is found as
// the peak of the window's power spectrum, which integrates the whole
// packet; that is the matched filter for a tone. The bin width is
// SampleRate/N (about 72 Hz at 2.36 MS/s), far finer than needed to tell
// 131 kHz channels apart. The window mean is removed and bins next to DC
// are skipped so the dongle's DC spike cannot win.
//
// ok is false when the packet lies outside the buffer or no bin stands out
// from the noise.
func (d Decoder) CarrierOffsetHz(idx, length int, mag []float32) (offset float64, ok bool) {
	if idx < 0 || length < 2 || idx+length > d.Cfg.BufferLength || (mag != nil && idx+length > len(mag)) {
		return 0, false
	}
	iq := d.IQ[idx<<1 : (idx+length)<<1]
	if carrierDump != "" {
		dumpWindow(d, idx, length, mag)
	}

	n := 1
	for n < length {
		n <<= 1
	}
	re := make([]float64, n)
	im := make([]float64, n)
	var meanI, meanQ float64
	for k := 0; k < length; k++ {
		meanI += float64(iq[k<<1])
		meanQ += float64(iq[k<<1|1])
	}
	meanI /= float64(length)
	meanQ /= float64(length)
	for k := 0; k < length; k++ {
		re[k] = float64(iq[k<<1]) - meanI
		im[k] = float64(iq[k<<1|1]) - meanQ
	}
	fft(re, im)

	fs := float64(d.Cfg.SampleRate)
	binHz := fs / float64(n)
	exclude := int(carrierDCExcludeHz / binHz)
	var total float64
	best, bestPow := -1, 0.0
	for k := 0; k < n; k++ {
		p := re[k]*re[k] + im[k]*im[k]
		total += p
		// Signed bin index; skip the DC neighbourhood.
		s := k
		if k >= n/2 {
			s = k - n
		}
		if s > -exclude && s < exclude {
			continue
		}
		if p > bestPow {
			best, bestPow = k, p
		}
	}
	if best < 0 {
		return 0, false
	}
	prominence := bestPow / (total / float64(n))
	if best >= n/2 {
		offset = float64(best-n) * binHz
	} else {
		offset = float64(best) * binHz
	}
	// Refine to a fraction of a bin with a parabola through the neighbours.
	if best > 0 && best < n-1 {
		l := math.Log(re[best-1]*re[best-1] + im[best-1]*im[best-1] + 1e-30)
		c := math.Log(bestPow + 1e-30)
		r := math.Log(re[best+1]*re[best+1] + im[best+1]*im[best+1] + 1e-30)
		den := l - 2*c + r
		if den < 0 {
			offset += 0.5 * (l - r) / den * binHz
		}
	}
	ok = prominence >= carrierMinProminence
	if carrierDebug {
		fmt.Fprintf(os.Stderr, "carrier: idx=%d len=%d n=%d peak %+.0f Hz prominence %.1f ok=%v\n",
			idx, length, n, offset, prominence, ok)
	}
	if !ok {
		return 0, false
	}
	return offset, true
}

// In-place iterative radix-2 FFT; len(re) == len(im) must be a power of two.
func fft(re, im []float64) {
	n := len(re)
	// Bit reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += size {
			cRe, cIm := 1.0, 0.0
			half := size >> 1
			for k := 0; k < half; k++ {
				a, b := start+k, start+k+half
				tRe := re[b]*cRe - im[b]*cIm
				tIm := re[b]*cIm + im[b]*cRe
				re[b], im[b] = re[a]-tRe, im[a]-tIm
				re[a], im[a] = re[a]+tRe, im[a]+tIm
				cRe, cIm = cRe*wRe-cIm*wIm, cRe*wIm+cIm*wRe
			}
		}
	}
}

func dumpWindow(d Decoder, idx, length int, mag []float32) {
	dumpSeq++
	// Include a symbol of context on each side.
	lo, hi := idx-d.Cfg.SymbolLength, idx+length+d.Cfg.SymbolLength
	if lo < 0 {
		lo = 0
	}
	if hi > d.Cfg.BufferLength {
		hi = d.Cfg.BufferLength
	}
	_ = os.WriteFile(fmt.Sprintf("%s/pkt%03d_idx%d_lo%d.iq", carrierDump, dumpSeq, idx, lo), d.IQ[lo<<1:hi<<1], 0o644)
	if mag == nil {
		return
	}
	f, err := os.Create(fmt.Sprintf("%s/pkt%03d_idx%d_lo%d.mag", carrierDump, dumpSeq, idx, lo))
	if err == nil {
		for _, v := range mag[lo:hi] {
			fmt.Fprintf(f, "%g\n", v)
		}
		f.Close()
	}
}
