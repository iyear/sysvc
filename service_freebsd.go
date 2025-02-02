// Copyright 2019 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package sysvc

import (
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/template"
)

//go:embed service_freebsd.tmpl
var freebsdScript string

const (
	platform  = "freebsd"
	configDir = "/usr/local/etc/rc.d"
)

type freebsdSystem struct{}

func (freebsdSystem) String() string { return platform }

func (freebsdSystem) Detect() bool { return true }

func (freebsdSystem) Interactive() bool { return interactive }

func (freebsdSystem) New(i Interface, c *Config) (Service, error) {
	s := &freebsdService{
		i:      i,
		Config: c,
	}

	return s, nil
}

func init() {
	ChooseSystem(freebsdSystem{})
}

var interactive = false

func init() {
	var err error
	interactive, err = isInteractive()
	if err != nil {
		panic(err)
	}
}

func isInteractive() (bool, error) {
	return os.Getenv("IS_DAEMON") != "1", nil
}

type freebsdService struct {
	i Interface
	*Config
}

func (s *freebsdService) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

func (s *freebsdService) Platform() string {
	return platform
}

func (s *freebsdService) template() *template.Template {
	functions := template.FuncMap{
		"bool": func(v bool) string {
			if v {
				return "true"
			}
			return "false"
		},
	}

	customConfig := s.Option.string(optionSysvScript, "")

	if customConfig != "" {
		return template.Must(template.New("").Funcs(functions).Parse(customConfig))
	} else {
		return template.Must(template.New("").Funcs(functions).Parse(freebsdScript))
	}
}

func (s *freebsdService) ConfigPath() (cp string, err error) {
	if oserr := os.MkdirAll(configDir, 0755); oserr != nil {
		err = oserr
		return
	}
	cp = filepath.Join(configDir, s.Config.Name)
	return
}

func (s *freebsdService) Install() error {
	path, err := s.execPath()
	if err != nil {
		return err
	}

	// write start script
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	_, err = os.Stat(confPath)
	if err == nil {
		return fmt.Errorf("Init already exists: %s", confPath)
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var to = &struct {
		*Config
		Path string
	}{
		s.Config,
		path,
	}

	err = s.template().Execute(f, to)
	if err != nil {
		return err
	}

	if err = os.Chmod(confPath, 0755); err != nil {
		return err
	}

	return nil
}

func (s *freebsdService) Uninstall() error {
	cp, err := s.ConfigPath()
	if err != nil {
		return err
	}
	return os.Remove(cp)
}

func (s *freebsdService) Status() (Status, error) {
	cp, err := s.ConfigPath()
	if err != nil {
		return StatusUnknown, err
	}

	if _, err = os.Stat(cp); os.IsNotExist(err) {
		return StatusStopped, ErrNotInstalled
	}

	status, _, err := runCommand("service", false, s.Name, "status")
	if status == 1 {
		return StatusStopped, nil
	} else if err != nil {
		return StatusUnknown, err
	}

	return StatusRunning, nil
}

func (s *freebsdService) Start() error {
	return run("service", s.Name, "start")
}

func (s *freebsdService) Stop() error {
	return run("service", s.Name, "stop")
}

func (s *freebsdService) Restart() error {
	return run("service", s.Name, "restart")
}

func (s *freebsdService) Run() error {
	if err := s.i.Start(s); err != nil {
		return err
	}

	s.Option.funcSingle(optionRunWait, func() {
		var sigChan = make(chan os.Signal, 3)
		signal.Notify(sigChan, syscall.SIGTERM, os.Interrupt)
		<-sigChan
	})()

	return s.i.Stop(s)
}

func (s *freebsdService) Logger(errs chan<- error) (Logger, error) {
	if interactive {
		return ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}

func (s *freebsdService) SystemLogger(errs chan<- error) (Logger, error) {
	return newSysLogger(s.Name, errs)
}
