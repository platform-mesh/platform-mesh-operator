/*
Copyright 2024.

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

	pmcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/golang-commons/traces"

	"github.com/platform-mesh/platform-mesh-operator/internal/controller"
	"github.com/platform-mesh/platform-mesh-operator/internal/controller/providers"
	"github.com/platform-mesh/platform-mesh-operator/internal/manager/aggregate"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "operator to setup platform-mesh",
	Run:   RunController,
}

func RunController(_ *cobra.Command, _ []string) { // coverage-ignore
	var err error

	ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())

	log.Info().Msg("Starting PlatformMesh Operator")
	defer log.Info().Msg("Shutting down PlatformMesh Operator")

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

	log.Info().Msg("Starting PlatformMesh manager")

	restCfg := ctrl.GetConfigOrDie()
	if operatorCfg.RemoteRuntime.IsEnabled() {
		setupLog.Info("Remote PlatformMesh reconciliation enabled, kubeconfig: " + operatorCfg.RemoteRuntime.Kubeconfig)
		var err error
		_, restCfg, err = subroutines.GetClientAndRestConfig(operatorCfg.RemoteRuntime.Kubeconfig)
		if err != nil {
			setupLog.Error(err, "unable to create PlatformMesh client")
			os.Exit(1)
		}
	}
	setupLog.Info(fmt.Sprintf("PlatformMesh Host: %s", restCfg.Host))
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

	platformMeshMgrOpts := mcmanager.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   defaultCfg.Metrics.BindAddress,
			SecureServing: defaultCfg.Metrics.Secure,
			TLSOpts:       tlsOpts,
		},
		BaseContext:                   func() context.Context { return ctx },
		HealthProbeBindAddress:        defaultCfg.HealthProbeBindAddress,
		LeaderElection:                defaultCfg.LeaderElectionEnabled,
		LeaderElectionID:              "81924e50.platform-mesh.org",
		LeaderElectionConfig:          leaderCfg,
		LeaderElectionReleaseOnCancel: true,
	}
	platformMeshMgr, err := mcmanager.New(restCfg, nil, platformMeshMgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start PlatformMesh manager")
		os.Exit(1)
	}

	log.Info().Msg("PlatformMesh manager successfully created")

	mgrs, err := aggregate.New(platformMeshMgr, platformMeshMgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to create aggregating manager")
		os.Exit(1)
	}

	restCfgInfra := ctrl.GetConfigOrDie()
	restCfgInfra.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(rt)
	})
	clientInfra, err := client.New(restCfgInfra, client.Options{Scheme: subroutines.GetClientScheme()})
	if err != nil {
		setupLog.Error(err, "unable to create Infra client")
		os.Exit(1)
	}
	if operatorCfg.RemoteInfra.IsEnabled() {
		var infraErr error
		clientInfra, _, infraErr = subroutines.GetClientAndRestConfig(operatorCfg.RemoteInfra.Kubeconfig)
		if infraErr != nil {
			setupLog.Error(infraErr, "unable to create Infra client")
			os.Exit(1)
		}
	}
	imageVersionStore := subroutines.NewImageVersionStore()

	pmReconciler, err := controller.NewPlatformMeshReconciler(platformMeshMgr, &operatorCfg, defaultCfg, operatorCfg.WorkspaceDir, clientInfra, imageVersionStore)
	if err != nil {
		setupLog.Error(err, "unable to create PlatformMesh reconciler")
		os.Exit(1)
	}
	if err := pmReconciler.SetupWithManager(platformMeshMgr, defaultCfg); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PlatformMesh")
		os.Exit(1)
	}

	resourceReconciler, err := controller.NewResourceReconciler(platformMeshMgr, &operatorCfg, clientInfra, imageVersionStore)
	if err != nil {
		setupLog.Error(err, "unable to create Resource reconciler")
		os.Exit(1)
	}
	if err := resourceReconciler.SetupWithManager(platformMeshMgr, defaultCfg); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Resource")
		os.Exit(1)
	}

	managedProvidersReconciler, err := providers.NewManagedProviderReconciler(platformMeshMgr, &operatorCfg, defaultCfg)
	if err != nil {
		setupLog.Error(err, "unable to create ManagedProvider reconciler")
		os.Exit(1)
	}
	if err := managedProvidersReconciler.SetupWithManager(platformMeshMgr, defaultCfg); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedProvider")
		os.Exit(1)
	}

	if err := platformMeshMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := platformMeshMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting aggregating manager")
	if err := mgrs.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatal().Err(err).Msg("problem running aggregating manager")
	}

}
