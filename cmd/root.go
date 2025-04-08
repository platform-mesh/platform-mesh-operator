package cmd

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	openmfpconfig "github.com/openmfp/golang-commons/config"
	"github.com/openmfp/golang-commons/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
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
	zc := zap.NewProductionConfig()
	z, err := zc.Build()
	if err != nil {
		panic(err)
	}
	ctrl.SetLogger(zapr.NewLogger(z))
	setupLog = ctrl.Log.WithName("setup") // coverage-ignore
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	setupLog.Info("init")
	rootCmd.AddCommand(operatorCmd)

	cobra.OnInitialize(initConfig, initLog)

	v, defaultCfg, err = openmfpconfig.NewDefaultConfig(rootCmd)
	if err != nil {
		panic(err)
	}
	//
	//err = openmfpconfig.BindConfigToFlags(v, operatorCmd, &operatorCfg)
	//if err != nil {
	//	panic(err)
	//}

}
func initConfig() {
	v.SetDefault("subroutines-provider-secret-enabled", true)
	v.SetDefault("subroutines-kcp-setup-enabled", true)
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
}

func Execute() { // coverage-ignore
	cobra.CheckErr(rootCmd.Execute())
}
