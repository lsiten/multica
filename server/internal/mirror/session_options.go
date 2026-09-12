package mirror

func NewSessionStore(options SessionStoreOptions) *SessionStore {
	clock := options.Clock
	if clock == nil {
		clock = wallClock{}
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultMirrorSessionTTL
	}
	if ttl > maxMirrorSessionTTL {
		ttl = maxMirrorSessionTTL
	}
	return &SessionStore{clock: clock, ttl: ttl, items: make(map[string]*storedSession)}
}
