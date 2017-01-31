package dispatcher

/*
 * Microservice gateway application
 * Copyright (C) 2015  Martin Helmich <m.helmich@mittwald.de>
 *                     Mittwald CM Service GmbH & Co. KG
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

import (
	"fmt"
	"github.com/julienschmidt/httprouter"
	"github.com/mittwald/servicegateway/config"
	"github.com/mittwald/servicegateway/proxy"
	"github.com/op/go-logging"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
)

type pathBasedDispatcher struct {
	abstractDispatcher
}

type PatternClosure struct {
	targetUrl  string
	parameters [][]string
	appName    string
	appCfg     *config.Application
	proxy      *proxy.ProxyHandler
}

type PathClosure struct {
	backendUrl string
	appName    string
	appCfg     *config.Application
	proxy      *proxy.ProxyHandler
}

func NewPathBasedDispatcher(
	cfg *config.Configuration,
	log *logging.Logger,
	prx *proxy.ProxyHandler,
) (*pathBasedDispatcher, error) {
	dispatcher := new(pathBasedDispatcher)
	dispatcher.cfg = cfg
	dispatcher.mux = httprouter.New()
	dispatcher.log = log
	dispatcher.prx = prx
	dispatcher.behaviours = make([]DispatcherBehaviour, 0, 8)

	return dispatcher, nil
}

func (d *pathBasedDispatcher) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	//	for k, v := range d.cfg.Proxy.SetResponseHeaders {
	//		res.Header.Set(k, v)
	//	}

	d.mux.ServeHTTP(res, req)
}

func (p *PatternClosure) Handle(rw http.ResponseWriter, req *http.Request, params httprouter.Params) {
	targetUrl := p.targetUrl
	for _, paramName := range p.parameters {
		targetUrl = strings.Replace(targetUrl, paramName[0], params.ByName(paramName[1]), -1)
	}

	p.proxy.HandleProxyRequest(rw, req, targetUrl, p.appName, p.appCfg)
}

func (p *PathClosure) Handle(rw http.ResponseWriter, req *http.Request, params httprouter.Params) {
	sanitizedPath := strings.Replace(req.URL.Path, p.appCfg.Routing.Path, "", 1)
	proxyUrl := p.backendUrl + sanitizedPath

	p.proxy.HandleProxyRequest(rw, req, proxyUrl, p.appName, p.appCfg)
}

func (d *pathBasedDispatcher) buildOptionsHandler(cfg *config.Application, inner httprouter.Handle) httprouter.Handle {
	return func(rw http.ResponseWriter, req *http.Request, params httprouter.Params) {
		recorder := httptest.NewRecorder()

		inner(recorder, req, params)

		allow := recorder.Header().Get("Allow")
		if allow == "" {
			allow = "GET, POST, PUT, DELETE, PATCH, OPTIONS"
		}

		if d.cfg.Proxy.OptionsConfiguration.CORS {
			recorder.Header().Set("Access-Control-Allow-Methods", allow)

			if recorder.Header().Get("Access-Control-Allow-Origin") == "" {
				recorder.Header().Set("Access-Control-Allow-Origin", "*")
			}

			if recorder.Header().Get("Access-Control-Allow-Credentials") == "" {
				recorder.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if recorder.Header().Get("Access-Control-Allow-Headers") == "" {
				recorder.Header().Set("Access-Control-Allow-Headers", "X-Requested-With, Authorization")
			}

			if recorder.Header().Get("Access-Control-Max-Age") == "" {
				recorder.Header().Set("Access-Control-Max-Age", "86400")
			}
		}

		for key, values := range recorder.Header() {
			for _, value := range values {
				rw.Header().Add(key, value)
			}
		}

		rw.Header().Set("Allow", allow)
		rw.WriteHeader(recorder.Code)
	}
}

func (d *pathBasedDispatcher) RegisterApplication(name string, appCfg config.Application) error {
	routes := make(map[string]httprouter.Handle)

	backendUrl := appCfg.Backend.URL
	if backendUrl == "" && appCfg.Backend.Service != "" {
		if appCfg.Backend.Tag != "" {
			backendUrl = fmt.Sprintf("http://%s.%s.service.consul", appCfg.Backend.Tag, appCfg.Backend.Service)
		} else {
			backendUrl = fmt.Sprintf("http://%s.service.consul", appCfg.Backend.Service)
		}
	}

	var rewriter proxy.HostRewriter

	if appCfg.Routing.Type == "path" {
		path := strings.TrimRight(appCfg.Routing.Path, "/")
		mapping := map[string]string{
			"/(?P<path>.*)": path + "/:path",
		}

		rewriter, _ = proxy.NewHostRewriter(backendUrl, mapping, d.log)

		closure := new(PathClosure)
		closure.backendUrl = backendUrl
		closure.appName = name
		closure.appCfg = &appCfg
		closure.proxy = d.prx

		routes[path] = closure.Handle
		routes[path+"/*path"] = closure.Handle
	} else if appCfg.Routing.Type == "pattern" {
		re := regexp.MustCompile(":([a-zA-Z0-9]+)")
		mapping := make(map[string]string)

		for pattern, target := range appCfg.Routing.Patterns {
			targetPattern := "^" + re.ReplaceAllString(target, "(?P<$1>[^/]+?)") + "$"
			mapping[targetPattern] = pattern

			parameters := re.FindAllStringSubmatch(pattern, -1)

			closure := new(PatternClosure)
			closure.targetUrl = backendUrl + target
			closure.parameters = parameters
			closure.appName = name
			closure.appCfg = &appCfg
			closure.proxy = d.prx

			routes[pattern] = closure.Handle
		}

		rewriter, _ = proxy.NewHostRewriter(backendUrl, mapping, d.log)
	}

	for route, handler := range routes {
		handler = rewriter.Decorate(handler)

		safeHandler := handler
		unsafeHandler := handler

		for _, behaviour := range d.behaviours {
			var err error
			safeHandler, unsafeHandler, err = behaviour.Apply(safeHandler, unsafeHandler, d, &appCfg)
			if err != nil {
				return err
			}
		}

		d.mux.GET(route, safeHandler)
		d.mux.HEAD(route, safeHandler)
		d.mux.POST(route, unsafeHandler)
		d.mux.PUT(route, unsafeHandler)
		d.mux.DELETE(route, unsafeHandler)

		// Register a dedicated OPTIONS handler if it was enabled.
		// If no OPTIONS handler was enabled, simply proxy OPTIONS request through to the backend servers.
		if d.cfg.Proxy.OptionsConfiguration.Enabled {
			d.mux.OPTIONS(route, d.buildOptionsHandler(&appCfg, safeHandler))
		} else {
			d.mux.OPTIONS(route, safeHandler)
		}
	}

	return nil
}

func (d *pathBasedDispatcher) Initialize() error {
	for _, behaviour := range d.behaviours {
		switch t := behaviour.(type) {
		case RoutingBehaviour:
			if err := t.AddRoutes(d.mux); err != nil {
				return err
			}
		}
	}

	return nil
}
