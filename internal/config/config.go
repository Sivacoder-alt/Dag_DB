package config

import (
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server struct {
		ListenAddr string `mapstructure:"listen_addr"`
	} `mapstructure:"server"`
	LevelDB struct {
		Path string `mapstructure:"path"`
	} `mapstructure:"leveldb"`
	Logging struct {
		Level  string `mapstructure:"level"`
		Output string `mapstructure:"output"`
		File   string `mapstructure:"file"`
	} `mapstructure:"logging"`
	DAG struct {
		MaxParents           int      `mapstructure:"max_parents"`
		DefaultWeight        float64  `mapstructure:"default_weight"`
		Peers                []string `mapstructure:"peers"`
		SyncInterval         int      `mapstructure:"sync_interval"`
		CheckpointInterval   int      `mapstructure:"checkpoint_interval"`
		ConfirmationThreshold float64  `mapstructure:"confirmation_threshold"`
	} `mapstructure:"dag"`
}

func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetEnvPrefix("DAG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if cfg.DAG.SyncInterval <= 0 {
		cfg.DAG.SyncInterval = 30
	}
	if cfg.DAG.CheckpointInterval <= 0 {
		cfg.DAG.CheckpointInterval = 300
	}
	if cfg.DAG.ConfirmationThreshold <= 0 {
		cfg.DAG.ConfirmationThreshold = 10.0
	}

	return &cfg, nil
}