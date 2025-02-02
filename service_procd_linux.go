// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package sysvc

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

// https://openwrt.org/docs/guide-developer/procd-init-scripts
//
//go:embed service_procd_linux.tmpl
var procdScript string

const (
	OptionRestartRespawnThreshold = "RestartRespawnThreshold"
	OptionRestartRespawnTimeout   = "RestartRespawnTimeout"
	OptionRestartRetry            = "RestartRetry"
)

func isProcd() bool {
	if _, err := exec.LookPath("procd"); err == nil {
		return true
	}
	return false
}

type procd struct {
	*sysv
	scriptPath string
}

func newProcdService(i Interface, platform string, c *Config) (Service, error) {
	sv := &sysv{
		i:        i,
		platform: platform,
		Config:   c,
	}

	p := &procd{
		sysv:       sv,
		scriptPath: "/etc/init.d/" + sv.Name,
	}
	return p, nil
}

func (p *procd) template() *template.Template {
	customScript := p.Option.string(optionSysvScript, "")

	if customScript != "" {
		return template.Must(template.New("").Funcs(tf).Parse(customScript))
	}
	return template.Must(template.New("").Funcs(tf).Parse(procdScript))
}

func (p *procd) Install() error {
	confPath, err := p.ConfigPath()
	if err != nil {
		return err
	}
	if _, err = os.Stat(confPath); err == nil {
		return fmt.Errorf("init already exists: %s", confPath)
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	path, err := p.execPath()
	if err != nil {
		return err
	}

	var to = &struct {
		*Config
		Path             string
		RespawnThreshold int
		RespawnTimeout   int
		RespawnRetry     int
	}{
		p.Config,
		path,
		p.Option.int(OptionRestartRespawnThreshold, 300),
		p.Option.int(OptionRestartRespawnTimeout, 5),
		p.Option.int(OptionRestartRetry, 10),
	}

	if err = p.template().Execute(f, to); err != nil {
		return err
	}

	if err = os.Chmod(confPath, 0755); err != nil {
		return err
	}

	if err = os.Symlink(confPath, "/etc/rc.d/S50"+p.Name); err != nil {
		return err
	}
	if err = os.Symlink(confPath, "/etc/rc.d/K02"+p.Name); err != nil {
		return err
	}

	return nil
}

func (p *procd) Uninstall() error {
	if err := run(p.scriptPath, "disable"); err != nil {
		return err
	}
	cp, err := p.ConfigPath()
	if err != nil {
		return err
	}
	if err = os.Remove(cp); err != nil {
		return err
	}
	return nil
}

func (p *procd) Status() (Status, error) {
	_, out, err := runWithOutput(p.scriptPath, "status")
	if err != nil && !(err.Error() == "exit status 3") {
		return StatusUnknown, err
	}

	switch {
	case strings.HasPrefix(out, "running"):
		return StatusRunning, nil
	case strings.HasPrefix(out, "inactive"):
		return StatusStopped, nil
	default:
		return StatusUnknown, ErrNotInstalled
	}
}

func (p *procd) Start() error {
	return run(p.scriptPath, "start")
}

func (p *procd) Stop() error {
	return run(p.scriptPath, "stop")
}

func (p *procd) Restart() error {
	if err := p.Stop(); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return p.Start()
}
