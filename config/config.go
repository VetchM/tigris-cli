// Copyright 2022 Tigris Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"bytes"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

var DefaultConfig = Config{
	URL:      "localhost:8081",
	Timeout:  5 * time.Second,
	Protocol: "grpc",
}

type Config struct {
	Token    string        `json:"token,omitempty"`
	URL      string        `json:"url,omitempty"`
	Timeout  time.Duration `json:"timeout,omitempty"`
	Protocol string        `json:"protocol,omitempty"`
}

var configPath = []string{
	"/etc/tigrisdata/",
	"$HOME/.tigrisdata",
	"./config/",
	"./",
}

var envPrefix = "tigrisdb"

func Load(name string, config interface{}) {
	viper.SetConfigName(name)

	for _, v := range configPath {
		viper.AddConfigPath(v)
	}

	// This is needed to automatically bind environment variables to config struct
	// Viper will only bind environment variables to the keys it already knows about
	b, err := yaml.Marshal(config)
	if err != nil {
		log.Err(err).Msg("marshal config")
	}

	// This is needed to replace periods with underscores when mapping environment variables to multi-level config keys
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// The environment variables have a higher priority as compared to config values defined in the config file.
	// This allows us to override the config values using environment variables.
	viper.SetEnvPrefix(envPrefix)
	viper.AutomaticEnv()

	br := bytes.NewBuffer(b)
	err = viper.MergeConfig(br)
	if err != nil {
		log.Err(err).Msg("merge config")
	}

	err = viper.MergeInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			//log.Warn().Err(err).Msgf("config file not found")
		} else {
			log.Fatal().Err(err).Msgf("error reading config")
		}
	}

	if err := viper.Unmarshal(&config); err != nil {
		log.Fatal().Err(err).Msg("error unmarshalling config")
	}
}
