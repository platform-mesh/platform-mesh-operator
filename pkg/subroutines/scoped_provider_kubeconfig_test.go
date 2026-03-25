package subroutines

import (
	"strings"
	"testing"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
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
		sliceName    string
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
			sliceName: "core.platform-mesh.io",
			wantName:  "core.platform-mesh.io",
			wantPath:  "root:platform-mesh-system",
		},
		{
			name: "empty spec.export.path defaults to platform workspace",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "core.platform-mesh.io",
					},
				},
			},
			sliceName: "core.platform-mesh.io",
			wantName:  "core.platform-mesh.io",
			wantPath:  platformMeshAPIExportWorkspace,
		},
		{
			name: "whitespace-only path treated as empty → default",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "x"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "some.export",
						Path: "   \t  ",
					},
				},
			},
			sliceName: "x",
			wantName:  "some.export",
			wantPath:  platformMeshAPIExportWorkspace,
		},
		{
			name: "trim name and path",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "slice"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{
						Name: "  my-export  ",
						Path: "  root:custom  ",
					},
				},
			},
			sliceName: "slice",
			wantName:  "my-export",
			wantPath:  "root:custom",
		},
		{
			name:         "empty spec.export.name",
			sliceName:    "named-slice",
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
			sliceName:    "any",
			wantErr:      true,
			errSubstring: "nil APIExportEndpointSlice",
		},
		{
			name: "sliceName empty uses metadata.name in error",
			slice: &kcpapiv1alpha1.APIExportEndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: "meta-name"},
				Spec: kcpapiv1alpha1.APIExportEndpointSliceSpec{
					APIExport: kcpapiv1alpha1.ExportBindingReference{},
				},
			},
			sliceName:    "",
			wantErr:      true,
			errSubstring: `APIExportEndpointSlice "meta-name"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotPath, err := apiExportLocationFromEndpointSlice(tt.slice, tt.sliceName)
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

func TestSanitizeSecretNameForRBAC(t *testing.T) {
	t.Parallel()
	longIn := strings.Repeat("x", maxRBACNameSuffixLength+50)
	longWant := strings.Repeat("x", maxRBACNameSuffixLength)
	tests := []struct {
		in   string
		want string
	}{
		{"kubernetes-graphql-gateway-kubeconfig", "kubernetes-graphql-gateway-kubeconfig"},
		{"My_Secret.Name", "my-secret-name"},
		{"rebac-authz-webhook-kubeconfig", "rebac-authz-webhook-kubeconfig"},
		{"", "scoped"},
		{"___", "scoped"},
		{longIn, longWant},
	}
	for _, tt := range tests {
		name := tt.in
		if len(name) > 40 {
			name = name[:20] + "…"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeSecretNameForRBAC(tt.in)
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
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
