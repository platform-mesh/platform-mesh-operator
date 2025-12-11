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
	"os"

	pmcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/spf13/cobra"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/platform-mesh/golang-commons/traces"

	"github.com/platform-mesh/platform-mesh-operator/internal/controller"
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

	restConfigPlatformMesh := ctrl.GetConfigOrDie()
	if operatorCfg.RemotePlatformMesh.Enabled {
		setupLog.Info("Remote PlatformMesh reconciliation enabled, kubeconfig: " + operatorCfg.RemotePlatformMesh.Kubeconfig)
		_, restConfigPlatformMesh, err = subroutines.GetClientAndRestConfig(operatorCfg.RemotePlatformMesh.Kubeconfig)
	}
	if err != nil {
		setupLog.Error(err, "unable to create PlatformMesh client")
		os.Exit(1)
	}
	setupLog.Info(fmt.Sprintf("PlatformMesh Host: %s", restConfigPlatformMesh.Host))
	// restConfigPlatformMesh.Wrap(func(rt http.RoundTripper) http.RoundTripper {
	// 	return otelhttp.NewTransport(rt)
	// })
	mgrPlatformMesh, err := ctrl.NewManager(restConfigPlatformMesh, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   defaultCfg.Metrics.BindAddress,
			SecureServing: defaultCfg.Metrics.Secure,
			TLSOpts:       tlsOpts,
		},
		BaseContext:                   func() context.Context { return ctx },
		HealthProbeBindAddress:        operatorCfg.RemotePlatformMesh.Metrics.HealthProbeBindAddress,
		LeaderElection:                operatorCfg.RemotePlatformMesh.Metrics.LeaderElectionEnabled,
		LeaderElectionID:              "81924e50-platformmesh.platform-mesh.org",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	restConfigFluxCD := ctrl.GetConfigOrDie()
	if operatorCfg.RemoteFluxCD.Enabled {
		setupLog.Info("Remote FluxCD deployment enabled")
		_, restConfigFluxCD, err = subroutines.GetClientAndRestConfig(operatorCfg.RemoteFluxCD.Kubeconfig)
	}
	if err != nil {
		setupLog.Error(err, "unable to create FluxCD client")
		os.Exit(1)
	}
	setupLog.Info(fmt.Sprintf("FluxCD Host: %s", restConfigFluxCD.Host))
	// restConfigFluxCD.Wrap(func(rt http.RoundTripper) http.RoundTripper {
	// 	return otelhttp.NewTransport(rt)
	// })
	mgrFluxCD, err := ctrl.NewManager(restConfigFluxCD, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   operatorCfg.RemoteFluxCD.Metrics.BindAddress, // ":9091",
			SecureServing: operatorCfg.RemoteFluxCD.Metrics.Secure,
			TLSOpts:       tlsOpts,
		},
		BaseContext:                   func() context.Context { return ctx },
		HealthProbeBindAddress:        operatorCfg.RemoteFluxCD.Metrics.HealthProbeBindAddress,
		LeaderElection:                operatorCfg.RemoteFluxCD.Metrics.LeaderElectionEnabled,
		LeaderElectionID:              "81924e50.platform-mesh.org",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start deploymentmanager")
		os.Exit(1)
	}

	pmReconciler := controller.NewPlatformMeshReconciler(log, mgrPlatformMesh, &operatorCfg, defaultCfg, operatorCfg.WorkspaceDir, mgrFluxCD.GetClient())
	if err := pmReconciler.SetupWithManager(mgrPlatformMesh, defaultCfg, log.ChildLogger("type", "PlatformMesh")); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PlatformMesh")
		os.Exit(1)
	}

	resourceReconciler := controller.NewResourceReconciler(log, mgrFluxCD, &operatorCfg)
	if err := resourceReconciler.SetupWithManager(mgrFluxCD, defaultCfg, log.ChildLogger("type", "Resource")); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PlatformMesh")
		os.Exit(1)
	}
	if operatorCfg.PatchOIDCControllerEnabled {
		realmReconciler := controller.NewRealmReconciler(mgrPlatformMesh, log, &operatorCfg)
		if err := realmReconciler.SetupWithManager(mgrPlatformMesh, defaultCfg, log.ChildLogger("type", "Realm")); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Realm")
			os.Exit(1)
		}
	}

	if err := mgrPlatformMesh.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgrPlatformMesh.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	signalHandler := ctrl.SetupSignalHandler()

	go func() {
		setupLog.Info("starting resource manager")
		if err := mgrFluxCD.Start(signalHandler); err != nil {
			log.Fatal().Err(err).Msg("problem running resource manager")
		}
	}()

	setupLog.Info("starting manager")
	if err := mgrPlatformMesh.Start(signalHandler); err != nil {
		log.Fatal().Err(err).Msg("problem running manager")
	}

}
