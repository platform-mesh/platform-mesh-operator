package cmd

import (
	"github.com/go-logr/logr"
	openmfpconfig "github.com/openmfp/golang-commons/config"
	"github.com/openmfp/golang-commons/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/internal/config"
)

var (
	scheme      = runtime.NewScheme()
	setupLog    logr.Logger
	operatorCfg config.OperatorConfig
	defaultCfg  *openmfpconfig.CommonServiceConfig
	v           *viper.Viper
	log         *logger.Logger
)

var rootCmd = &cobra.Command{
	Use:   "openmfp-operator",
	Short: "operator to setup OpenMFP",
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	rootCmd.AddCommand(operatorCmd)

	var err error
	v, defaultCfg, err = openmfpconfig.NewDefaultConfig(rootCmd)
	if err != nil {
		panic(err)
	}

	cobra.OnInitialize(initConfig)

	err = openmfpconfig.BindConfigToFlags(v, operatorCmd, &operatorCfg)
	if err != nil {
		panic(err)
	}

	cobra.OnInitialize(initLog)
}

func initConfig() {
	v.SetDefault("subroutines-deployment-enabled", true)
	v.SetDefault("subroutines-kcp-setup-enabled", true)
	v.SetDefault("subroutines-provider-secret-enabled", true)
	v.SetDefault("workspace-dir", "/operator/")

}

func initLog() { // coverage-ignore
	logcfg := logger.DefaultConfig()
	logcfg.Level = defaultCfg.Log.Level
	logcfg.NoJSON = defaultCfg.Log.NoJson

	var err error
	log, err = logger.New(logcfg)
	if err != nil {
		panic(err)
	}
	ctrl.SetLogger(log.Logr())
	setupLog = ctrl.Log.WithName("setup") // coverage-ignore
}

func Execute() { // coverage-ignore
	cobra.CheckErr(rootCmd.Execute())
}
