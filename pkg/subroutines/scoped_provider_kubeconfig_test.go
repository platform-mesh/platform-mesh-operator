package subroutines

import (
	"context"
	"net/url"
	"strings"
	"testing"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

func TestVirtualWorkspacePathFromSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		slice   *kcpapiv1alpha1.APIExportEndpointSlice
		want    string
		wantErr bool
	}{
		{
			name: "kind local-setup (root.kcp.localhost) — path segment is workspace logical cluster id, varies per cluster",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://root.kcp.localhost:8443/services/apiexport/158ffh0myu3e6xhu/core.platform-mesh.io"},
					},
				},
			},
			want: "/services/apiexport/158ffh0myu3e6xhu/core.platform-mesh.io",
		},
		{
			name: "in-cluster front-proxy host from working-state reference",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://frontproxy-front-proxy.platform-mesh-system:6443/services/apiexport/2n6dxtatafypkpsg/core.platform-mesh.io"},
					},
				},
			},
			want: "/services/apiexport/2n6dxtatafypkpsg/core.platform-mesh.io",
		},
		{
			name: "path with wildcard clusters suffix from kcp",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://shard.internal:6443/services/apiexport/abc123/core.platform-mesh.io/clusters/%2A"},
					},
				},
			},
			// net/url decodes %2A in Path to '*'; kubeconfig server string uses this decoded form.
			want: "/services/apiexport/abc123/core.platform-mesh.io/clusters/*",
		},
		{
			name: "trailing slash on URL path trimmed",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "x"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://h:6443/services/apiexport/id/export-name/"},
					},
				},
			},
			want: "/services/apiexport/id/export-name",
		},
		{
			name: "first endpoint wins",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "multi"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://a:1/services/apiexport/first/export"},
						{URL: "https://b:2/services/apiexport/second/export"},
					},
				},
			},
			want: "/services/apiexport/first/export",
		},
		{
			name:    "nil slice",
			slice:   nil,
			wantErr: true,
		},
		{
			name: "no endpoints",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "empty"},
			},
			wantErr: true,
		},
		{
			name: "invalid URL",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "bad"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "://nohost"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "URL with only host no path",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "nopath"},
				Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
					APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
						{URL: "https://only.host:6443"},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := virtualWorkspacePathFromSlice(tt.slice)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("path: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestJoinVirtualWorkspaceServerURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		hostPort string
		rawPath  string
		want     string
	}{
		{
			name:     "front-proxy plus slice path from cluster capture",
			hostPort: "https://frontproxy-front-proxy.platform-mesh-system:6443",
			rawPath:  "/services/apiexport/2n6dxtatafypkpsg/core.platform-mesh.io",
			want:     "https://frontproxy-front-proxy.platform-mesh-system:6443/services/apiexport/2n6dxtatafypkpsg/core.platform-mesh.io",
		},
		{
			name:     "hostPort without trailing slash rawPath absolute",
			hostPort: "https://fp.ns.svc:6443",
			rawPath:  "/services/apiexport/x/y",
			want:     "https://fp.ns.svc:6443/services/apiexport/x/y",
		},
		{
			name:     "hostPort with trailing slash",
			hostPort: "https://fp.ns.svc:6443/",
			rawPath:  "/services/apiexport/x/y",
			want:     "https://fp.ns.svc:6443/services/apiexport/x/y",
		},
		{
			name:     "rawPath without leading slash",
			hostPort: "https://h:6443",
			rawPath:  "services/apiexport/a/b",
			want:     "https://h:6443/services/apiexport/a/b",
		},
		{
			name:     "path with wildcard segment after slice parse",
			hostPort: "https://front-proxy:6443",
			rawPath:  "/services/apiexport/id/name/clusters/*",
			want:     "https://front-proxy:6443/services/apiexport/id/name/clusters/*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := joinVirtualWorkspaceServerURL(tt.hostPort, tt.rawPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("server URL: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestVirtualWorkspacePathAndJoinRoundTrip(t *testing.T) {
	t.Parallel()
	// Same shape as local kind + in-cluster operator: slice URL from kcp, server URL the operator writes.
	slice := &kcpapiv1alpha1.APIExportEndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
		Status: kcpapiv1alpha1.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha1.APIExportEndpoint{
				{URL: "https://root.kcp.localhost:8443/services/apiexport/158ffh0myu3e6xhu/core.platform-mesh.io"},
			},
		},
	}
	rawPath, err := virtualWorkspacePathFromSlice(slice)
	if err != nil {
		t.Fatal(err)
	}
	hostPort := "https://frontproxy-front-proxy.platform-mesh-system:6443"
	got, err := joinVirtualWorkspaceServerURL(hostPort, rawPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://frontproxy-front-proxy.platform-mesh-system:6443/services/apiexport/158ffh0myu3e6xhu/core.platform-mesh.io"
	if got != want {
		t.Fatalf("round-trip server URL: got %q want %q", got, want)
	}
}

func TestAPIExportLocationFromEndpointSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		slice        *kcpapiv1alpha1.APIExportEndpointSlice
		wantName     string
		wantPath     string
		wantErr      bool
		errSubstring string
	}{
		{
			name: "local cluster core slice (spec from kubectl get … -o yaml)",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "core.platform-mesh.io",
						Path: "root:platform-mesh-system",
					},
				},
			},
			wantName: "core.platform-mesh.io",
			wantPath: "root:platform-mesh-system",
		},
		{
			name:         "empty spec.export.path",
			wantErr:      true,
			errSubstring: "empty spec.export.path",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "core.platform-mesh.io",
					},
				},
			},
		},
		{
			name:     "spec values returned as stored (no trim)",
			wantName: "  my-export  ",
			wantPath: "  root:custom  ",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "slice"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "  my-export  ",
						Path: "  root:custom  ",
					},
				},
			},
		},
		{
			name:         "empty spec.export.name",
			wantErr:      true,
			errSubstring: `empty spec.export.name`,
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "named-slice"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{},
				},
			},
		},
		{
			name:         "nil slice",
			slice:        nil,
			wantErr:      true,
			errSubstring: "nil APIExportEndpointSlice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotPath, err := apiExportLocationFromEndpointSlice(tt.slice)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errSubstring != "" && !strings.Contains(err.Error(), tt.errSubstring) {
					t.Fatalf("error %q should contain %q", err.Error(), tt.errSubstring)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gotName != tt.wantName || gotPath != tt.wantPath {
				t.Fatalf("got name=%q path=%q want name=%q path=%q", gotName, gotPath, tt.wantName, tt.wantPath)
			}
		})
	}
}

func TestBuildKCPConfigForPath(t *testing.T) {
	t.Parallel()
	cfg := &rest.Config{Host: "https://shard:6443/clusters/root:orgs:ws"}
	out := buildKCPConfigForPath(cfg, "root:platform-mesh-system")
	if out.Host != "https://shard:6443/clusters/root:platform-mesh-system" {
		t.Fatalf("Host: got %q", out.Host)
	}
	cfgBare := &rest.Config{Host: "shard:6443"}
	outBare := buildKCPConfigForPath(cfgBare, "root:x")
	if outBare.Host != "https://shard:6443/clusters/root:x" {
		t.Fatalf("Host (bare): got %q", outBare.Host)
	}
}

func TestResolveAPIExportVirtualWorkspaceRawPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := &rest.Config{Host: "https://kcp:6443"}
	t.Run("empty slice name", func(t *testing.T) {
		t.Parallel()
		_, err := resolveAPIExportVirtualWorkspaceRawPath(ctx, &Helper{}, cfg, "root:platform-mesh-system", "")
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("expected empty name error, got %v", err)
		}
	})
}

func TestWorkspaceClusterScopedServerURLJoinPath(t *testing.T) {
	t.Parallel()
	// Same shape as writeScopedKubeconfigToSecret when apiExportName is set (no endpoint slice).
	hostPort := "https://frontproxy-front-proxy.platform-mesh-system:6443"
	pcPath := "root:platform-mesh-system"
	got, err := url.JoinPath(hostPort, "clusters", pcPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://frontproxy-front-proxy.platform-mesh-system:6443/clusters/root:platform-mesh-system"
	if got != want {
		t.Fatalf("server URL: got %q want %q", got, want)
	}
}

func TestParseScopedKubeconfigExportSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		pc          corev1alpha1.ProviderConnection
		wantSlice   string
		wantExport  string
		wantErr     bool
		errContains string
	}{
		{
			name: "apiExportName only",
			pc: corev1alpha1.ProviderConnection{
				APIExportName: ptr.To("core.platform-mesh.io"),
			},
			wantExport: "core.platform-mesh.io",
		},
		{
			name: "endpointSliceName only",
			pc: corev1alpha1.ProviderConnection{
				EndpointSliceName: ptr.To("core.platform-mesh.io"),
			},
			wantSlice: "core.platform-mesh.io",
		},
		{
			name: "trim whitespace",
			pc: corev1alpha1.ProviderConnection{
				APIExportName: ptr.To("  my-export  "),
			},
			wantExport: "my-export",
		},
		{
			name:        "both set",
			pc:          corev1alpha1.ProviderConnection{EndpointSliceName: ptr.To("a"), APIExportName: ptr.To("b")},
			wantErr:     true,
			errContains: "only one",
		},
		{
			name:        "neither set",
			pc:          corev1alpha1.ProviderConnection{},
			wantErr:     true,
			errContains: "requires endpointSliceName or apiExportName",
		},
		{
			name:        "both whitespace",
			pc:          corev1alpha1.ProviderConnection{EndpointSliceName: ptr.To("  "), APIExportName: ptr.To("\t")},
			wantErr:     true,
			errContains: "requires endpointSliceName or apiExportName",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSlice, gotExport, err := parseScopedKubeconfigExportSource(tt.pc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q should contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gotSlice != tt.wantSlice || gotExport != tt.wantExport {
				t.Fatalf("got slice=%q export=%q want slice=%q export=%q", gotSlice, gotExport, tt.wantSlice, tt.wantExport)
			}
		})
	}
}
