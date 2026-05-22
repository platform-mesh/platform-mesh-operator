/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	pmcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/spf13/cobra"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/golang-commons/traces"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/platform-mesh/platform-mesh-operator/internal/controller/providers"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

var providersCmd = &cobra.Command{
	Use:   "providers",
	Short: "provider bootstrap controller watching kcp via providers.platform-mesh.io APIExport virtual workspace",
	Run:   RunProviders,
}

func buildKcpAdminConfigForWorkspace(restCfg *rest.Config, wsPath string) (*rest.Config, error) {
	c, err := client.New(restCfg, client.Options{})
	if err != nil {
		return nil, err
	}
	kcpUrl := providersCfg.KCP.Url
	if kcpUrl == "" {
		kcpUrl = fmt.Sprintf("https://%s-front-proxy.%s:%s", providersCfg.KCP.FrontProxyName, providersCfg.KCP.Namespace, providersCfg.KCP.FrontProxyPort)
	}
	kcpUrl += fmt.Sprintf("/clusters/%s", wsPath)
	return pmsubs.BuildKcpAdminConfig(c, &providersCfg.KCP, kcpUrl)
}

func RunProviders(_ *cobra.Command, _ []string) { // coverage-ignore
	var err error

	ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())

	log.Info().Msg("Starting Platform Mesh Providers Controller")
	defer log.Info().Msg("Shutting down Platform Mesh Providers Controller")

	ctx, _, shutdown := pmcontext.StartContext(log, operatorCfg, defaultCfg.ShutdownTimeout)
	defer shutdown()

	disableHTTP2 := func(c *tls.Config) {
		log.Info().Msg("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !defaultCfg.EnableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	var providerShutdown func(ctx context.Context) error
	if defaultCfg.Tracing.Enabled {
		providerShutdown, err = traces.InitProvider(ctx, defaultCfg.Tracing.Collector)
		if err != nil {
			log.Fatal().Err(err).Msg("unable to start gRPC-Sidecar TracerProvider")
		}
	} else {
		providerShutdown, err = traces.InitLocalProvider(ctx, defaultCfg.Tracing.Collector, false)
		if err != nil {
			log.Fatal().Err(err).Msg("unable to start local TracerProvider")
		}
	}

	defer func() {
		if err := providerShutdown(ctx); err != nil {
			log.Fatal().Err(err).Msg("failed to shutdown TracerProvider")
		}
	}()

	log.Info().Msg("Starting manager")

	restCfg := ctrl.GetConfigOrDie()
	restCfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(rt)
	})

	var leaderCfg *rest.Config
	if defaultCfg.LeaderElectionEnabled {
		leaderCfg, err = rest.InClusterConfig()
		if err != nil {
			log.Fatal().Err(err).Msg("unable to get in-cluster config")
		}
	}

	providersEndpointSliceCfg, err := buildKcpAdminConfigForWorkspace(restCfg, providersCfg.ProvidersAPIExportEndpointSliceWorkspace)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create kcp admin client config")
	}

	log.Info().Msgf("Created client config for %q", providersEndpointSliceCfg.Host)

	providersVW, err := apiexport.New(providersEndpointSliceCfg, providersCfg.ProvidersAPIExportEndpointSliceName, apiexport.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create apiexport provider")
	}

	mgr, err := mcmanager.New(providersEndpointSliceCfg, providersVW, mcmanager.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   defaultCfg.Metrics.BindAddress,
			SecureServing: defaultCfg.Metrics.Secure,
			TLSOpts:       tlsOpts,
		},
		BaseContext:                   func() context.Context { return ctx },
		HealthProbeBindAddress:        defaultCfg.HealthProbeBindAddress,
		LeaderElection:                defaultCfg.LeaderElectionEnabled,
		LeaderElectionID:              "93035f61.platform-mesh.org",
		LeaderElectionConfig:          leaderCfg,
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	log.Info().Msg("Manager successfully started")

	rec, err := providers.NewProviderReconciler(mgr, &providersCfg, defaultCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create ProviderReconciler")
	}
	if err := rec.SetupWithManager(mgr, defaultCfg); err != nil {
		log.Fatal().Err(err).Msg("unable to setup ProviderReconciler with manager")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatal().Err(err).Msg("unable to set up health check")
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatal().Err(err).Msg("unable to set up ready check")
	}

	setupLog.Info("starting providers manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatal().Err(err).Msg("problem running providers manager")
	}
}
