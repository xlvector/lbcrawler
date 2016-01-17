package lb

type UpstreamConfig struct {
	Addr string `json:"addr"`
}

type ServiceConfig struct {
	Upstreams []*UpstreamConfig `json:"upstreams"`
}
