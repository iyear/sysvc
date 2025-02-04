// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package sysvc

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"text/template"
	"time"
)

//go:embed service_darwin.tmpl
var launchdConfig string

const (
	platform                  = "darwin-launchd"
	defaultDarwinLogDirectory = "/var/log"
)

type darwinSystem struct{}

func (darwinSystem) String() string { return platform }

func (darwinSystem) Detect() bool { return true }

func (darwinSystem) Interactive() bool { return interactive }

func (darwinSystem) New(i Interface, c *Config) (Service, error) {
	s := &darwinLaunchdService{
		i:      i,
		Config: c,

		userService: c.Option.bool(optionUserService, optionUserServiceDefault),
	}

	return s, nil
}

func init() {
	ChooseSystem(darwinSystem{})
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
	// TODO: The PPID of Launchd is 1. The PPid of a service process should match launchd's PID.
	return os.Getppid() != 1, nil
}

type darwinLaunchdService struct {
	i Interface
	*Config

	userService bool
}

func (s *darwinLaunchdService) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

func (s *darwinLaunchdService) Platform() string {
	return platform
}

func (s *darwinLaunchdService) getHomeDir() (string, error) {
	u, err := user.Current()
	if err == nil {
		return u.HomeDir, nil
	}

	// alternate methods
	homeDir := os.Getenv("HOME") // *nix
	if homeDir == "" {
		return "", errors.New("User home directory not found.")
	}
	return homeDir, nil
}

func (s *darwinLaunchdService) ConfigPath() (string, error) {
	if s.userService {
		homeDir, err := s.getHomeDir()
		if err != nil {
			return "", err
		}
		return homeDir + "/Library/LaunchAgents/" + s.Name + ".plist", nil
	}
	return "/Library/LaunchDaemons/" + s.Name + ".plist", nil
}

func (s *darwinLaunchdService) logDir() (string, error) {
	if customDir := s.Option.string(optionLogDirectory, ""); customDir != "" {
		return customDir, nil
	}
	if !s.userService {
		return defaultDarwinLogDirectory, nil
	}
	return s.getHomeDir()
}

func (s *darwinLaunchdService) getLogPaths() (string, string, error) {
	logDir, err := s.logDir()
	if err != nil {
		return "", "", err
	}
	return s.getLogPath(logDir, "out"), s.getLogPath(logDir, "err"), nil
}

func (s *darwinLaunchdService) getLogPath(logDir, logType string) string {
	return fmt.Sprintf("%s/%s.%s.log", logDir, s.Name, logType)
}

func (s *darwinLaunchdService) template() *template.Template {
	functions := template.FuncMap{
		"bool": func(v bool) string {
			if v {
				return "true"
			}
			return "false"
		},
	}

	customConfig := s.Option.string(optionLaunchdConfig, "")

	if customConfig != "" {
		return template.Must(template.New("").Funcs(functions).Parse(customConfig))
	}
	return template.Must(template.New("").Funcs(functions).Parse(launchdConfig))
}

func (s *darwinLaunchdService) Install() error {
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	_, err = os.Stat(confPath)
	if err == nil {
		return fmt.Errorf("Init already exists: %s", confPath)
	}

	if s.userService {
		// Ensure that ~/Library/LaunchAgents exists.
		err = os.MkdirAll(filepath.Dir(confPath), 0700)
		if err != nil {
			return err
		}
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	path, err := s.execPath()
	if err != nil {
		return err
	}

	stdOutPath, stdErrPath, _ := s.getLogPaths()
	var to = &struct {
		*Config
		Path string

		KeepAlive, RunAtLoad bool
		SessionCreate        bool
		StandardOutPath      string
		StandardErrorPath    string
	}{
		Config:            s.Config,
		Path:              path,
		KeepAlive:         s.Option.bool(optionKeepAlive, optionKeepAliveDefault),
		RunAtLoad:         s.Option.bool(optionRunAtLoad, optionRunAtLoadDefault),
		SessionCreate:     s.Option.bool(optionSessionCreate, optionSessionCreateDefault),
		StandardOutPath:   stdOutPath,
		StandardErrorPath: stdErrPath,
	}

	return s.template().Execute(f, to)
}

func (s *darwinLaunchdService) Uninstall() error {
	_ = s.Stop() // stop the service if it is running

	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	return os.Remove(confPath)
}

func (s *darwinLaunchdService) Status() (Status, error) {
	exitCode, out, err := runWithOutput("launchctl", "list", s.Name)
	if exitCode == 0 && err != nil {
		if !strings.Contains(err.Error(), "failed with stderr") {
			return StatusUnknown, err
		}
	}

	re := regexp.MustCompile(`"PID" = ([0-9]+);`)
	matches := re.FindStringSubmatch(out)
	if len(matches) == 2 {
		return StatusRunning, nil
	}

	confPath, err := s.ConfigPath()
	if err != nil {
		return StatusUnknown, err
	}

	if _, err = os.Stat(confPath); err == nil {
		return StatusStopped, nil
	}

	return StatusUnknown, ErrNotInstalled
}

func (s *darwinLaunchdService) Start() error {
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	return run("launchctl", "load", confPath)
}

func (s *darwinLaunchdService) Stop() error {
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	return run("launchctl", "unload", confPath)
}

func (s *darwinLaunchdService) Restart() error {
	err := s.Stop()
	if err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return s.Start()
}

func (s *darwinLaunchdService) Run() error {
	err := s.i.Start(s)
	if err != nil {
		return err
	}

	s.Option.funcSingle(optionRunWait, func() {
		var sigChan = make(chan os.Signal, 3)
		signal.Notify(sigChan, syscall.SIGTERM, os.Interrupt)
		<-sigChan
	})()

	return s.i.Stop(s)
}

func (s *darwinLaunchdService) Logger(errs chan<- error) (Logger, error) {
	if interactive {
		return ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}

func (s *darwinLaunchdService) SystemLogger(errs chan<- error) (Logger, error) {
	return newSysLogger(s.Name, errs)
}
