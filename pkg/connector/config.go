package connector

import (
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	_ "embed"

	up "go.mau.fi/util/configupgrade"
)

//go:embed example-config.yaml
var ExampleConfig string

type Config struct {
	DisplaynameTemplate string             `yaml:"displayname_template"`
	displaynameTemplate *template.Template `yaml:"-"`

	Onebot struct {
		Endpoint       string        `yaml:"endpoint"`
		RequestTimeout time.Duration `yaml:"request_timeout"`
	} `yaml:"onebot"`
}

type umConfig Config

func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	err := node.Decode((*umConfig)(c))
	if err != nil {
		return err
	}
	return c.PostProcess()
}

func (c *Config) PostProcess() error {
	// 防止错误的配置导致"context deadline exceeded"
	if c.Onebot.RequestTimeout <= 0 {
		c.Onebot.RequestTimeout = 60 * time.Second
	}

	var err error
	c.displaynameTemplate, err = template.New("displayname").Parse(c.DisplaynameTemplate)
	return err
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "displayname_template")

	helper.Copy(up.Str, "onebot", "endpoint")
	helper.Copy(up.Str, "onebot", "request_timeout")
}

func (pc *PylonConnector) GetConfig() (example string, data any, upgrader up.Upgrader) {
	return ExampleConfig, &pc.Config, up.SimpleUpgrader(upgradeConfig)
}

type DisplaynameParams struct {
	Alias string
	Name  string
	ID    string
}

func (c *Config) FormatDisplayname(params DisplaynameParams) string {
	var buffer strings.Builder
	_ = c.displaynameTemplate.Execute(&buffer, params)
	return buffer.String()
}
