package lb

import (
	"bytes"
	"encoding/json"
	"github.com/xlvector/dlog"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

type DownStream struct {
	up        *UpstreamConfig
	used      bool
	sid       string
	startTime time.Time
}

type ServiceHandler struct {
	srv           *ServiceConfig
	freeUpstreams chan *UpstreamConfig
	cache         map[string]*DownStream
	ticker        *time.Ticker
}

func NewServiceHandler(srv *ServiceConfig) *ServiceHandler {
	ret := &ServiceHandler{
		srv:           srv,
		freeUpstreams: make(chan *UpstreamConfig, 100),
		cache:         make(map[string]*DownStream),
		ticker:        time.NewTicker(time.Second * 3),
	}
	for _, up := range srv.Upstreams {
		dlog.Println("append up: ", up.Addr)
		ret.freeUpstreams <- up
	}
	go func() {
		for t := range ret.ticker.C {
			dlog.Warn("close timeout upstream: %d", t.Unix())
			del := []*DownStream{}
			for _, d := range ret.cache {
				if d.used && time.Now().Sub(d.startTime).Seconds() > 300 {
					d.used = false
					del = append(del, d)
					continue
				}
				if !d.used && time.Now().Sub(d.startTime).Seconds() > 120 {
					del = append(del, d)
					continue
				}
			}

			for _, d := range del {
				dlog.Println("free upstream: ", d.up.Addr)
				delete(ret.cache, d.sid)
				ret.freeUpstreams <- d.up
			}
		}
	}()
	return ret
}

func randString() string {
	str := strconv.FormatInt(time.Now().UnixNano(), 10)
	str += "_"
	str += strconv.FormatInt(rand.Int63(), 10)
	return str
}

func (p *ServiceHandler) closeDownstream(down *DownStream) {
	delete(p.cache, down.sid)
	p.freeUpstreams <- down.up
}

func (p *ServiceHandler) createDownstream(req *http.Request) *DownStream {
	if sid, err := req.Cookie("lbc_session_id"); err == nil && sid != nil {
		dlog.Println("recv sid: ", sid.Value)
		if down, ok := p.cache[sid.Value]; ok {
			if isClose, err2 := req.Cookie("lbc_close"); err2 == nil && isClose != nil {
				p.closeDownstream(down)
				return nil
			}
			return down
		}
		dlog.Println("fail to find sid: ", sid.Value, p.cache)
		return nil
	} else {
		down := &DownStream{
			up:        <-p.freeUpstreams,
			sid:       randString(),
			startTime: time.Now(),
		}
		dlog.Println("use up: ", down.up.Addr)
		p.cache[down.sid] = down
		return down
	}
}

type LoadBalancer struct {
	services map[string]*ServiceHandler
	client   *http.Client
}

func NewLoadBalancer(conf string) *LoadBalancer {
	b, err := ioutil.ReadFile(conf)
	if err != nil {
		dlog.Error("fail to read config: %v", err)
		return nil
	}
	srvs := make(map[string]*ServiceConfig)
	err = json.Unmarshal(b, &srvs)
	if err != nil {
		dlog.Error("fail to decode json: %v", err)
	}

	ret := &LoadBalancer{
		services: make(map[string]*ServiceHandler),
		client:   &http.Client{},
	}

	for k, v := range srvs {
		ret.services[k] = NewServiceHandler(v)
	}
	return ret
}

func (p *LoadBalancer) printError(rw http.ResponseWriter, down *DownStream, msg string) {
	dlog.Warn(msg)
	rw.WriteHeader(500)
	rw.Write([]byte(msg))
	if down != nil {
		down.used = false
	}
}

func (p *LoadBalancer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	srv, ok := p.services[req.Host]
	if !ok {
		p.printError(rw, nil, "fail to get service: "+req.Host)
		return
	}

	down := srv.createDownstream(req)
	if nil == down {
		dlog.Warn("fail to find downstream")
		rw.WriteHeader(500)
		p.printError(rw, nil, "fail to find downstream")
		return
	}
	down.used = true

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		p.printError(rw, down, "fail to read body: "+err.Error())
		return
	}
	req2, err := http.NewRequest(req.Method, "http://"+down.up.Addr+req.RequestURI, bytes.NewReader(body))
	dlog.Println(req2.Method, " ", req2.URL.String())
	if err != nil {
		p.printError(rw, down, "new req2 fail: "+err.Error())
		return
	}
	resp, err := p.client.Do(req2)
	if err != nil {
		p.printError(rw, down, "call upstream failed: "+err.Error())
		return
	}

	if resp.Body == nil {
		p.printError(rw, down, "upstream resp body is nil")
		return
	}

	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		p.printError(rw, down, "read upstream body fail: "+err.Error())
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name:  "lbc_session_id",
		Value: down.sid,
	})
	rw.WriteHeader(200)
	rw.Write(b)
	down.used = false
}
