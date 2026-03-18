// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"tailscale.com/ipn"
	"tailscale.com/kube/kubetypes"
	"tailscale.com/kube/localclient"
	"tailscale.com/tailcfg"
)

func TestUpdateServeConfig(t *testing.T) {
	tests := []struct {
		name       string
		sc         *ipn.ServeConfig
		certDomain string
		wantCall   bool
	}{
		{
			name: "no_https_no_cert_domain",
			sc: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80: {HTTP: true},
				},
			},
			certDomain: kubetypes.ValueNoHTTPS, // tailnet has HTTPS disabled
			wantCall:   true,                   // should set serve config as it doesn't have HTTPS endpoints
		},
		{
			name: "https_with_cert_domain",
			sc: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					443: {HTTPS: true},
				},
				Web: map[ipn.HostPort]*ipn.WebServerConfig{
					"${TS_CERT_DOMAIN}:443": {
						Handlers: map[string]*ipn.HTTPHandler{
							"/": {Proxy: "http://10.0.1.100:8080"},
						},
					},
				},
			},
			certDomain: "test-node.tailnet.ts.net",
			wantCall:   true,
		},
		{
			name: "https_without_cert_domain",
			sc: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					443: {HTTPS: true},
				},
			},
			certDomain: kubetypes.ValueNoHTTPS,
			wantCall:   false, // incorrect configuration- should not set serve config
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeLC := &localclient.FakeLocalClient{}
			err := updateServeConfig(context.Background(), tt.sc, tt.certDomain, fakeLC)
			if err != nil {
				t.Errorf("updateServeConfig() error = %v", err)
			}
			if fakeLC.SetServeCalled != tt.wantCall {
				t.Errorf("SetServeConfig() called = %v, want %v", fakeLC.SetServeCalled, tt.wantCall)
			}
		})
	}
}

func TestReadServeConfig(t *testing.T) {
	tests := []struct {
		name       string
		gotSC      string
		certDomain string
		wantSC     *ipn.ServeConfig
		wantErr    bool
	}{
		{
			name: "empty_file",
		},
		{
			name: "valid_config_with_cert_domain_placeholder",
			gotSC: `{
				"TCP": {
					"443": {
						"HTTPS": true
					}
				},
				"Web": {
					"${TS_CERT_DOMAIN}:443": {
					"Handlers": {
						"/api": {
							"Proxy": "https://10.2.3.4/api"
						}}}}}`,
			certDomain: "example.com",
			wantSC: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					443: {
						HTTPS: true,
					},
				},
				Web: map[ipn.HostPort]*ipn.WebServerConfig{
					ipn.HostPort("example.com:443"): {
						Handlers: map[string]*ipn.HTTPHandler{
							"/api": {
								Proxy: "https://10.2.3.4/api",
							},
						},
					},
				},
			},
		},
		{
			name: "valid_config_for_http_proxy",
			gotSC: `{
				"TCP": {
					"80": {
						"HTTP": true
					}
				}}`,
			wantSC: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80: {
						HTTP: true,
					},
				},
			},
		},
		{
			name: "config_without_cert_domain",
			gotSC: `{
				"TCP": {
					"443": {
						"HTTPS": true
					}
				},
				"Web": {
					"localhost:443": {
					"Handlers": {
						"/api": {
							"Proxy": "https://10.2.3.4/api"
						}}}}}`,
			certDomain: "",
			wantErr:    false,
			wantSC: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					443: {
						HTTPS: true,
					},
				},
				Web: map[ipn.HostPort]*ipn.WebServerConfig{
					ipn.HostPort("localhost:443"): {
						Handlers: map[string]*ipn.HTTPHandler{
							"/api": {
								Proxy: "https://10.2.3.4/api",
							},
						},
					},
				},
			},
		},
		{
			name:    "invalid_json",
			gotSC:   "invalid json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "serve-config.json")
			if err := os.WriteFile(path, []byte(tt.gotSC), 0644); err != nil {
				t.Fatal(err)
			}

			got, err := readServeConfig(path, tt.certDomain)
			if (err != nil) != tt.wantErr {
				t.Errorf("readServeConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !cmp.Equal(got, tt.wantSC) {
				t.Errorf("readServeConfig() diff (-got +want):\n%s", cmp.Diff(got, tt.wantSC))
			}
		})
	}
}

func TestRefreshAdvertiseServices(t *testing.T) {
	tests := []struct {
		name                string
		sc                  *ipn.ServeConfig
		wantServices        []string
		wantEditPrefsCalled bool
		wantErr             bool
	}{
		{
			name:                "nil_serve_config",
			sc:                  nil,
			wantEditPrefsCalled: false,
		},
		{
			name:                "empty_serve_config",
			sc:                  &ipn.ServeConfig{},
			wantEditPrefsCalled: false,
		},
		{
			name: "no_services_defined",
			sc: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80: {HTTP: true},
				},
			},
			wantEditPrefsCalled: false,
		},
		{
			name: "single_service",
			sc: &ipn.ServeConfig{
				Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{
					"svc:my-service": {},
				},
			},
			wantServices:        []string{"svc:my-service"},
			wantEditPrefsCalled: true,
		},
		{
			name: "multiple_services",
			sc: &ipn.ServeConfig{
				Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{
					"svc:service-a": {},
					"svc:service-b": {},
					"svc:service-c": {},
				},
			},
			wantServices:        []string{"svc:service-a", "svc:service-b", "svc:service-c"},
			wantEditPrefsCalled: true,
		},
		{
			name: "services_with_tcp_and_web",
			sc: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80: {HTTP: true},
				},
				Web: map[ipn.HostPort]*ipn.WebServerConfig{
					"example.com:443": {},
				},
				Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{
					"svc:frontend": {},
					"svc:backend":  {},
				},
			},
			wantServices:        []string{"svc:frontend", "svc:backend"},
			wantEditPrefsCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeLC := &localclient.FakeLocalClient{}
			err := refreshAdvertiseServices(context.Background(), tt.sc, fakeLC)

			if (err != nil) != tt.wantErr {
				t.Errorf("refreshAdvertiseServices() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantEditPrefsCalled != (len(fakeLC.EditPrefsCalls) > 0) {
				t.Errorf("EditPrefs called = %v, want %v", len(fakeLC.EditPrefsCalls) > 0, tt.wantEditPrefsCalled)
			}

			if tt.wantEditPrefsCalled {
				if len(fakeLC.EditPrefsCalls) != 1 {
					t.Fatalf("expected 1 EditPrefs call, got %d", len(fakeLC.EditPrefsCalls))
				}

				mp := fakeLC.EditPrefsCalls[0]
				if !mp.AdvertiseServicesSet {
					t.Error("AdvertiseServicesSet should be true")
				}

				if len(mp.AdvertiseServices) != len(tt.wantServices) {
					t.Errorf("AdvertiseServices length = %d, want %d", len(mp.Prefs.AdvertiseServices), len(tt.wantServices))
				}

				advertised := make(map[string]bool)
				for _, svc := range mp.AdvertiseServices {
					advertised[svc] = true
				}

				for _, want := range tt.wantServices {
					if !advertised[want] {
						t.Errorf("expected service %q to be advertised, but it wasn't", want)
					}
				}
			}
		})
	}
}

func TestHasHTTPSEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ipn.ServeConfig
		want bool
	}{
		{
			name: "nil_config",
			cfg:  nil,
			want: false,
		},
		{
			name: "empty_config",
			cfg:  &ipn.ServeConfig{},
			want: false,
		},
		{
			name: "no_https_endpoints",
			cfg: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80: {
						HTTPS: false,
					},
				},
			},
			want: false,
		},
		{
			name: "has_https_endpoint",
			cfg: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					443: {
						HTTPS: true,
					},
				},
			},
			want: true,
		},
		{
			name: "mixed_endpoints",
			cfg: &ipn.ServeConfig{
				TCP: map[uint16]*ipn.TCPPortHandler{
					80:  {HTTPS: false},
					443: {HTTPS: true},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasHTTPSEndpoint(tt.cfg)
			if got != tt.want {
				t.Errorf("hasHTTPSEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadSimpleServeConfig(t *testing.T) {
	certDomain := "example.com"

	tests := []struct {
		name      string
		configs   []string
		wantErr   bool
		assertion func(t *testing.T, sc *ipn.ServeConfig)
	}{
		{
			name:    "empty configs returns nil",
			configs: nil,
			wantErr: false,
			assertion: func(t *testing.T, sc *ipn.ServeConfig) {
				if sc != nil {
					t.Fatalf("expected nil ServeConfig, got %#v", sc)
				}
			},
		},
		{
			name:    "simple https config on default port",
			configs: []string{"https://localhost:3000"},
			assertion: func(t *testing.T, sc *ipn.ServeConfig) {
				if sc == nil {
					t.Fatal("expected ServeConfig, got nil")
				}

				if _, ok := sc.TCP[443]; !ok {
					t.Fatalf("expected TCP config on port 443")
				}

				hp := ipn.HostPort("example.com:443")
				web, ok := sc.Web[hp]
				if !ok {
					t.Fatalf("expected web config for %s", hp)
				}

				handler, ok := web.Handlers["/"]
				if !ok {
					t.Fatalf("expected handler for path '/'")
				}

				if handler.Proxy != "https://localhost:3000" {
					t.Fatalf("unexpected proxy value: %s", handler.Proxy)
				}

				if sc.AllowFunnel[hp] {
					t.Fatalf("funnel should be disabled by default")
				}
			},
		},
		{
			name:    "https with funnel enabled",
			configs: []string{"funnel; https://localhost:3000"},
			assertion: func(t *testing.T, sc *ipn.ServeConfig) {
				hp := ipn.HostPort("example.com:443")
				if !sc.AllowFunnel[hp] {
					t.Fatalf("expected funnel to be enabled")
				}
			},
		},
		{
			name:    "custom allowed port tcp8443",
			configs: []string{"8443; https://localhost:3000"},
			assertion: func(t *testing.T, sc *ipn.ServeConfig) {
				if _, ok := sc.TCP[8443]; !ok {
					t.Fatalf("expected TCP config on port 8443")
				}
			},
		},
		{
			name:    "invalid funnel port",
			configs: []string{"funnel 9999; https://localhost:3000"},
			wantErr: true,
		},
		{
			name: "duplicate port conflict",
			configs: []string{
				"https://localhost:3000",
				"https://localhost:4000",
			},
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			configs: []string{"udp://localhost:3000"},
			wantErr: true,
		},
		{
			name:    "tcp forwarding config",
			configs: []string{"tcp://127.0.0.1:22"},
			assertion: func(t *testing.T, sc *ipn.ServeConfig) {
				handler, ok := sc.TCP[22]
				if !ok {
					t.Fatalf("expected TCP handler on port 22")
				}

				if handler.TCPForward != "127.0.0.1:22" {
					t.Fatalf("unexpected TCPForward value: %s", handler.TCPForward)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, err := readSimpleServeConfig(tt.configs, certDomain)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.assertion != nil {
				tt.assertion(t, sc)
			}
		})
	}
}
