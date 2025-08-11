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
		MaxParents    int     `mapstructure:"max_parents"`
		DefaultWeight float64 `mapstructure:"default_weight"`
	} `mapstructure:"dag"`
}

func LoadConfig(configPath string) (*Config, error) {
    v := viper.New()
    v.SetConfigName("config")
    v.SetConfigType("yaml")
    v.AddConfigPath(configPath)       // Add the provided path
    v.AddConfigPath("../../config")   // Add the relative path to the config directory
    v.AddConfigPath(".")              // Add the current directory as a fallback
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
    return &cfg, nil
}
