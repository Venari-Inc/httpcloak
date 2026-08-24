package pool

import (
	crand "crypto/rand"
	"encoding/binary"

	"github.com/sardanioss/httpcloak/fingerprint"
	utls "github.com/sardanioss/utls"
)

func tcpClientHelloSpec(preset *fingerprint.Preset, id utls.ClientHelloID, seed int64) (*utls.ClientHelloSpec, error) {
	return fingerprint.SpecFor(id, seed, preset.SignatureAlgorithms)
}

func quicClientHelloSpec(preset *fingerprint.Preset, id utls.ClientHelloID, seed int64) (*utls.ClientHelloSpec, error) {
	return fingerprint.SpecFor(id, seed, preset.QUICSignatureAlgorithms)
}

// newClientHelloShuffleSeed draws one ClientHello extension-shuffle seed. It is
// called once per connection: Chrome permutes its extension order on every
// connection (shipped in Chrome 110), so a seed that outlives a single
// connection would pin one order for every connection that reuses it.
func newClientHelloShuffleSeed() int64 {
	var seedBytes [8]byte
	crand.Read(seedBytes[:])
	return int64(binary.LittleEndian.Uint64(seedBytes[:]))
}
