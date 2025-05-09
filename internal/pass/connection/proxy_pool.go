package connection

import (
	"sync"
	"time"
)

type proxyPool struct {
	mu      sync.Mutex
	proxies map[string]*proxyPair
}

// getOrCreateProxyPair registers proxyPair if not existing in pool and returns it.
func (pp *proxyPool) getOrCreateProxyPair(id string, disconnectAfter time.Duration) *proxyPair {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	v, ok := pp.proxies[id]
	if !ok {
		v = initProxyPair(disconnectAfter)
	}
	pp.proxies[id] = v
	return v
}

func (pp *proxyPool) deleteExpiresPairs() {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	for key, pair := range pp.proxies {
		if time.Now().After(pair.expiresAt) {
			delete(pp.proxies, key)
		}
	}
}

func (pp *proxyPool) deleteProxyPair(id string) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	// Channels inside proxyPair are closed in proxy.readPump and proxy.writePump.
	delete(pp.proxies, id)
}

type proxyPair struct {
	toMobileDataCh    *safeChannel
	toExtensionDataCh *safeChannel
	expiresAt         time.Time
}

// initProxyPair returns proxyPair and runs loop responsible for proxing data.
func initProxyPair(disconnectAfter time.Duration) *proxyPair {
	return &proxyPair{
		toMobileDataCh:    newSafeChannel(),
		toExtensionDataCh: newSafeChannel(),
		expiresAt:         time.Now().Add(disconnectAfter + time.Minute),
	}
}

type safeChannel struct {
	channel chan []byte
	mu      *sync.Mutex
}

func newSafeChannel() *safeChannel {
	return &safeChannel{
		channel: make(chan []byte),
		mu:      &sync.Mutex{},
	}
}

func (sc *safeChannel) Write(data []byte) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.channel == nil {
		return
	}

	sc.channel <- data
}

func (sc *safeChannel) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.channel == nil {
		return
	}

	close(sc.channel)
	sc.channel = nil
}
