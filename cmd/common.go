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
	"fmt"
	"os"

	"github.com/openmfp/golang-commons/logger"
	"github.com/openmfp/openmfp-operator/internal/config"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func initApp() (config.Config, *logger.Logger) {
	appConfig, err := config.NewFromEnv()
	if err != nil {
		fmt.Printf("Error loading env file: %v\n", err) // nolint: forbidigo
		os.Exit(1)
	}

	logConfig := logger.DefaultConfig()
	logConfig.Name = "openmfp-operator"
	logConfig.Level = appConfig.Log.Level
	logConfig.NoJSON = appConfig.IsLocal
	log, err := logger.New(logConfig)
	if err != nil {
		fmt.Printf("Error init logger: %v\n", err) // nolint: forbidigo
		os.Exit(1)
	}

	log.Info().Msgf("Logging on log level: %s", log.GetLevel().String())

	return appConfig, log
}
