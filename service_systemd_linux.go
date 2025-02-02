// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package sysvc

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/template"
)

//go:embed service_systemd_linux.tmpl
var systemdConfig string

func isSystemd() bool {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return true
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	if _, err := os.Stat("/proc/1/comm"); err == nil {
		filerc, err := os.Open("/proc/1/comm")
		if err != nil {
			return false
		}
		defer filerc.Close()

		buf := new(bytes.Buffer)
		buf.ReadFrom(filerc)
		contents := buf.String()

		if strings.Trim(contents, " \r\n") == "systemd" {
			return true
		}
	}
	return false
}

type systemd struct {
	i        Interface
	platform string
	*Config
}

func newSystemdService(i Interface, platform string, c *Config) (Service, error) {
	s := &systemd{
		i:        i,
		platform: platform,
		Config:   c,
	}

	return s, nil
}

func (s *systemd) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

func (s *systemd) Platform() string {
	return s.platform
}

func (s *systemd) ConfigPath() (cp string, err error) {
	if !s.isUserService() {
		cp = "/etc/systemd/system/" + s.unitName()
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	systemdUserDir := filepath.Join(homeDir, ".config/systemd/user")
	err = os.MkdirAll(systemdUserDir, os.ModePerm)
	if err != nil {
		return
	}
	cp = filepath.Join(systemdUserDir, s.unitName())
	return
}

func (s *systemd) unitName() string {
	return s.Config.Name + ".service"
}

func (s *systemd) getSystemdVersion() int64 {
	_, out, err := s.runWithOutput("systemctl", "--version")
	if err != nil {
		return -1
	}

	re := regexp.MustCompile(`systemd ([0-9]+)`)
	matches := re.FindStringSubmatch(out)
	if len(matches) != 2 {
		return -1
	}

	v, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return -1
	}

	return v
}

func (s *systemd) hasOutputFileSupport() bool {
	if version := s.getSystemdVersion(); version < 236 {
		return false
	}

	return true
}

func (s *systemd) template() *template.Template {
	customScript := s.Option.string(optionSystemdScript, "")

	if customScript != "" {
		return template.Must(template.New("").Funcs(tf).Parse(customScript))
	}
	return template.Must(template.New("").Funcs(tf).Parse(systemdConfig))
}

func (s *systemd) isUserService() bool {
	return s.Option.bool(optionUserService, optionUserServiceDefault)
}

func (s *systemd) Install() error {
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	if _, err = os.Stat(confPath); err == nil {
		return fmt.Errorf("Init already exists: %s", confPath)
	}

	f, err := os.OpenFile(confPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	path, err := s.execPath()
	if err != nil {
		return err
	}

	var to = &struct {
		*Config
		Path                 string
		HasOutputFileSupport bool
		ReloadSignal         string
		PIDFile              string
		LimitNOFILE          int
		Restart              string
		SuccessExitStatus    string
		LogOutput            bool
		LogDirectory         string
		RestartSec           int
	}{
		s.Config,
		path,
		s.hasOutputFileSupport(),
		s.Option.string(optionReloadSignal, ""),
		s.Option.string(optionPIDFile, ""),
		s.Option.int(optionLimitNOFILE, optionLimitNOFILEDefault),
		s.Option.string(optionRestart, "always"),
		s.Option.string(optionSuccessExitStatus, ""),
		s.Option.bool(optionLogOutput, optionLogOutputDefault),
		s.Option.string(optionLogDirectory, defaultLogDirectory),
		s.Option.int(optionRestartSec, optionRestartSecDefault),
	}

	err = s.template().Execute(f, to)
	if err != nil {
		return err
	}

	err = s.runAction("enable")
	if err != nil {
		return err
	}

	return s.run("daemon-reload")
}

func (s *systemd) Uninstall() error {
	if err := s.runAction("disable"); err != nil {
		return err
	}

	cp, err := s.ConfigPath()
	if err != nil {
		return err
	}

	if err = os.Remove(cp); err != nil {
		return err
	}
	return s.run("daemon-reload")
}

func (s *systemd) Logger(errs chan<- error) (Logger, error) {
	if system.Interactive() {
		return ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}
func (s *systemd) SystemLogger(errs chan<- error) (Logger, error) {
	return newSysLogger(s.Name, errs)
}

func (s *systemd) Run() error {
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

func (s *systemd) Status() (Status, error) {
	exitCode, out, err := s.runWithOutput("systemctl", "is-active", s.unitName())
	if exitCode == 0 && err != nil {
		return StatusUnknown, err
	}

	switch {
	case strings.HasPrefix(out, "active"):
		return StatusRunning, nil
	case strings.HasPrefix(out, "inactive"):
		// inactive can also mean its not installed, check unit files
		exitCode, out, err = s.runWithOutput("systemctl", "list-unit-files", "-t", "service", s.unitName())
		if exitCode == 0 && err != nil {
			return StatusUnknown, err
		}
		if strings.Contains(out, s.Name) {
			// unit file exists, installed but not running
			return StatusStopped, nil
		}
		// no unit file
		return StatusUnknown, ErrNotInstalled
	case strings.HasPrefix(out, "activating"):
		return StatusRunning, nil
	case strings.HasPrefix(out, "failed"):
		return StatusUnknown, errors.New("service in failed state")
	default:
		return StatusUnknown, ErrNotInstalled
	}
}

func (s *systemd) Start() error {
	return s.runAction("start")
}

func (s *systemd) Stop() error {
	return s.runAction("stop")
}

func (s *systemd) Restart() error {
	return s.runAction("restart")
}

func (s *systemd) runWithOutput(command string, arguments ...string) (int, string, error) {
	if s.isUserService() {
		arguments = append(arguments, "--user")
	}
	return runWithOutput(command, arguments...)
}

func (s *systemd) run(action string, args ...string) error {
	if s.isUserService() {
		return run("systemctl", append([]string{action, "--user"}, args...)...)
	}
	return run("systemctl", append([]string{action}, args...)...)
}

func (s *systemd) runAction(action string) error {
	return s.run(action, s.unitName())
}
