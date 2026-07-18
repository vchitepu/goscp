package main

import (
	stderrs "errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	_ "github.com/vchitepu/goscp/internal/deps"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var version = "dev"

type cliOptions struct {
	Connections  int
	SFTPSessions int
	ChunkSize    string
	LogOutput    string
	LogFile      string
	LimitMbps    int
	Identity     string
	SSHConfig    string
	Login        string
	Port         int
	SSHOptions   []string
	IPv4         bool
	IPv6         bool
	Compress     bool
	Checkpoint   bool
	Resume       string
	DryRun       bool
	Quiet        bool
	Verbose      int
	ShowVersion  bool
}

type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func Execute(logger *logrus.Logger) error {
	if logger == nil {
		logger = logrus.New()
		logger.SetOutput(os.Stdout)
	}

	rootCmd, err := newRootCommand(logger)
	if err != nil {
		return errors.Wrap(err, "initialize root command")
	}

	if err := rootCmd.Execute(); err != nil {
		var ee *exitError
		if stderrs.As(err, &ee) {
			if ee.Err != nil {
				return errors.Wrap(ee.Err, "execute goscp")
			}
			if ee.Code != 0 {
				return errors.Errorf("goscp exited with code %d", ee.Code)
			}
			return nil
		}
		return errors.Wrap(err, "execute goscp")
	}
	return nil
}

func newRootCommand(logger *logrus.Logger) (*cobra.Command, error) {
	opts := &cliOptions{}

	cmd := &cobra.Command{
		Use:           "goscp [flags] <src>... <dst>",
		Short:         "GoSCP - fast concurrent SCP",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyViperOptions(opts)
			logFile, err := configureLogger(opts, logger)
			if err != nil {
				return &exitError{Code: 2, Err: errors.Wrap(err, "configure logger")}
			}
			if logFile != nil {
				defer func() {
					_ = logFile.Close()
				}()
			}
			if opts.ShowVersion {
				fmt.Fprintln(cmd.OutOrStdout(), version)
				return nil
			}

			if len(args) < 2 {
				return &exitError{Code: 2, Err: errors.New("requires at least one source and one destination")}
			}

			code, err := RunTransfer(cmd.Context(), logger, opts, args)
			if err != nil {
				return &exitError{Code: code, Err: errors.Wrap(err, "run transfer")}
			}
			if code != 0 {
				return &exitError{Code: code}
			}
			return nil
		},
	}

	defaultConnections := runtime.NumCPU() / 4
	if defaultConnections < 1 {
		defaultConnections = 1
	}

	flags := cmd.Flags()
	flags.IntVarP(&opts.Connections, "connections", "n", defaultConnections, "number of concurrent SSH connections")
	flags.IntVarP(&opts.SFTPSessions, "sftp-sessions", "s", 1, "number of SFTP sessions per SSH connection")
	flags.StringVarP(&opts.ChunkSize, "chunk-size", "S", "auto", "chunk size (e.g. 128M, default auto)")
	flags.StringVar(&opts.LogOutput, "log-output", "stdout", "log output destination: stdout|stderr")
	flags.StringVar(&opts.LogFile, "log-file", "", "write logs to file path (overrides --log-output)")
	flags.IntVarP(&opts.LimitMbps, "limit", "L", 0, "bandwidth limit in Mbps (0 = unlimited)")
	flags.StringVarP(&opts.Identity, "identity", "i", "", "identity (private key) file")
	flags.StringVarP(&opts.SSHConfig, "ssh-config", "F", "", "ssh config file")
	flags.StringVarP(&opts.Login, "login", "l", "", "login name")
	flags.IntVarP(&opts.Port, "port", "P", 22, "SSH port")
	flags.StringArrayVarP(&opts.SSHOptions, "ssh-option", "o", nil, "SSH options (repeatable)")
	flags.BoolVarP(&opts.IPv4, "ipv4", "4", false, "force IPv4")
	flags.BoolVarP(&opts.IPv6, "ipv6", "6", false, "force IPv6")
	flags.BoolVarP(&opts.Compress, "compress", "C", false, "enable compression")
	flags.BoolVarP(&opts.Checkpoint, "checkpoint", "c", false, "enable checkpointing")
	flags.StringVarP(&opts.Resume, "resume", "R", "", "resume from checkpoint ID")
	flags.BoolVarP(&opts.DryRun, "dry-run", "D", false, "print transfer plan and exit")
	flags.BoolVarP(&opts.Quiet, "quiet", "q", false, "quiet mode")
	verbosePtr := flags.CountP("verbose", "v", "increase verbosity (repeatable)")
	flags.BoolVar(&opts.ShowVersion, "version", false, "print version")

	if err := initConfigAndBindings(flags); err != nil {
		return nil, err
	}

	cmd.PreRun = func(_ *cobra.Command, _ []string) {
		opts.Verbose = *verbosePtr
	}

	cmd.SetVersionTemplate("{{.Version}}\n")
	return cmd, nil
}

func initConfigAndBindings(flags *pflag.FlagSet) error {
	viper.SetEnvPrefix("GOSCP")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "resolve home dir")
	}
	configFile := filepath.Join(homeDir, ".goscp", "config.yaml")
	viper.SetConfigFile(configFile)
	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !stderrs.As(err, &notFound) && !os.IsNotExist(err) {
			return errors.Wrapf(err, "read config %s", configFile)
		}
	}

	var bindErr error
	flags.VisitAll(func(f *pflag.Flag) {
		if err := viper.BindPFlag(f.Name, f); err != nil && bindErr == nil {
			bindErr = errors.Wrapf(err, "bind flag %q", f.Name)
		}
	})
	if bindErr != nil {
		return bindErr
	}

	return nil
}

func applyViperOptions(opts *cliOptions) {
	opts.Connections = viper.GetInt("connections")
	opts.SFTPSessions = viper.GetInt("sftp-sessions")
	opts.ChunkSize = viper.GetString("chunk-size")
	opts.LogOutput = viper.GetString("log-output")
	opts.LogFile = viper.GetString("log-file")
	opts.LimitMbps = viper.GetInt("limit")
	opts.Identity = viper.GetString("identity")
	opts.SSHConfig = viper.GetString("ssh-config")
	opts.Login = viper.GetString("login")
	opts.Port = viper.GetInt("port")
	opts.SSHOptions = append([]string(nil), viper.GetStringSlice("ssh-option")...)
	opts.IPv4 = viper.GetBool("ipv4")
	opts.IPv6 = viper.GetBool("ipv6")
	opts.Compress = viper.GetBool("compress")
	opts.Checkpoint = viper.GetBool("checkpoint")
	opts.Resume = viper.GetString("resume")
	opts.DryRun = viper.GetBool("dry-run")
	opts.Quiet = viper.GetBool("quiet")
	opts.ShowVersion = viper.GetBool("version")
}
