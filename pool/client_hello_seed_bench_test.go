package pool

import "testing"

// BenchmarkClientHelloShuffleSeedDraw measures the only work the per-connection
// seed adds: one 8-byte crypto/rand draw per connection. The spec rebuild it
// feeds already ran per connection before the change, so this is the whole
// delta a handshake pays.
func BenchmarkClientHelloShuffleSeedDraw(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = newClientHelloShuffleSeed()
	}
}

// BenchmarkHostPoolSpecBuildPerConn measures the full per-connection spec build
// the seed draw sits inside, so the draw can be read as a fraction of the work
// that was already there.
func BenchmarkHostPoolSpecBuildPerConn(b *testing.B) {
	t := &testing.T{}
	server := startClientHelloServer(t)
	preset := deterministicSpecPreset(t)
	pool := newSeedTestPool(t, preset, server, 4242)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pool.clientHelloSpecForConn(newClientHelloShuffleSeed()); err != nil {
			b.Fatal(err)
		}
	}
}
