package cli

import (
	"fmt"
	"os"
	"regexp"

	"github.com/klothoplatform/klotho/pkg/cli_config"

	"github.com/klothoplatform/klotho/pkg/updater"

	"github.com/klothoplatform/klotho/pkg/input"

	"github.com/fatih/color"
	"github.com/klothoplatform/klotho/pkg/analytics"
	"github.com/klothoplatform/klotho/pkg/config"
	"github.com/klothoplatform/klotho/pkg/core"
	"github.com/klothoplatform/klotho/pkg/logging"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type KlothoMain struct {
	DefaultUpdateStream string
	Version             string
	VersionQualifier    string
	PluginSetup         func(*PluginSetBuilder) error
}

var cfg struct {
	verbose       bool
	config        string
	outDir        string
	ast           bool
	caps          bool
	provider      string
	appName       string
	strict        bool
	disableLogo   bool
	internalDebug bool
	version       bool
	uploadSource  bool
	update        bool
	cfgFormat     string
	login         string
	setOption     map[string]string
}

var hadWarnings = atomic.NewBool(false)
var hadErrors = atomic.NewBool(false)

func init() {
	err := zap.RegisterEncoder("klotho-cli", func(zcfg zapcore.EncoderConfig) (zapcore.Encoder, error) {
		return logging.NewConsoleEncoder(cfg.verbose, hadWarnings, hadErrors), nil
	})

	if err != nil {
		panic(err)
	}
}

const (
	defaultOutDir = "compiled"
)

func (km KlothoMain) Main() {

	var root = &cobra.Command{
		Use:  "klotho [path to source]",
		RunE: km.run,
	}

	flags := root.Flags()

	flags.BoolVarP(&cfg.verbose, "verbose", "v", false, "Verbose flag")
	flags.StringVarP(&cfg.config, "config", "c", "", "Config file")
	flags.StringVarP(&cfg.outDir, "outDir", "o", defaultOutDir, "Output directory")
	flags.BoolVar(&cfg.ast, "ast", false, "Print the AST to a companion file")
	flags.BoolVar(&cfg.caps, "caps", false, "Print the capabilities to a companion file")
	flags.StringVarP(&cfg.cfgFormat, "cfg-format", "F", "yaml", "The format for the compiled config file (if --config is not specified). Supports: yaml, toml, json")
	flags.StringVar(&cfg.appName, "app", "", "Application name")
	flags.StringVarP(&cfg.provider, "provider", "p", "", fmt.Sprintf("Provider to compile to. Supported: %v", "aws"))
	flags.BoolVar(&cfg.strict, "strict", false, "Fail the compilation on warnings")
	flags.BoolVar(&cfg.disableLogo, "disable-logo", false, "Disable printing the Klotho logo")
	flags.BoolVar(&cfg.uploadSource, "upload-source", false, "Upload the compressed source folder for debugging")
	flags.BoolVar(&cfg.internalDebug, "internalDebug", false, "Enable debugging for compiler")
	flags.BoolVar(&cfg.version, "version", false, "Print the version")
	flags.BoolVar(&cfg.update, "update", false, "update the cli to the latest version")
	flags.StringVar(&cfg.login, "login", "", "Login to Klotho with email. For anonymous login, use 'local'")
	flags.StringToStringVar(&cfg.setOption, "set-option", nil, "Sets a CLI option")
	_ = flags.MarkHidden("internalDebug")

	err := root.Execute()
	if err != nil {
		if cfg.internalDebug {
			zap.S().With(logging.SendEntryMessage).Errorf("%+v", err)
		} else if !root.SilenceErrors {
			zap.S().With(logging.SendEntryMessage).Errorf("%v", err)
		}
		zap.S().With(logging.SendEntryMessage).Error("Klotho compilation failed")
		os.Exit(1)
	}
	if hadWarnings.Load() && cfg.strict {
		os.Exit(1)
	}
}

func setupLogger(analyticsClient *analytics.Client) (*zap.Logger, error) {
	var zapCfg zap.Config
	if cfg.verbose {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}
	zapCfg.Encoding = "klotho-cli"
	return zapCfg.Build(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		trackingCore := analyticsClient.NewFieldListener(zapcore.WarnLevel)
		return zapcore.NewTee(core, trackingCore)
	}))
}

func readConfig(args []string) (appCfg config.Application, err error) {
	if cfg.config != "" {
		appCfg, err = config.ReadConfig(cfg.config)
		if err != nil {
			return
		}
	} else {
		appCfg.Format = cfg.cfgFormat
	}
	// TODO debug logging for when config file is overwritten by CLI flags
	if cfg.appName != "" {
		appCfg.AppName = cfg.appName
	}
	if cfg.provider != "" {
		appCfg.Provider = cfg.provider
	}
	if len(args) > 0 {
		appCfg.Path = args[0]
	}
	if cfg.outDir != "" {
		if appCfg.OutDir == "" || cfg.outDir != defaultOutDir {
			appCfg.OutDir = cfg.outDir
		}
	}

	return
}

func (km KlothoMain) run(cmd *cobra.Command, args []string) (err error) {
	// color.NoColor is set if we're not a terminal that
	// supports color
	if !color.NoColor && !cfg.disableLogo {
		color.New(color.FgHiGreen).Println(Logo)
		fmt.Println()
	}

	// create config directory if necessary, must run
	// before calling analytics for first time
	if err := cli_config.CreateKlothoConfigPath(); err != nil {
		zap.S().Warnf("failed to create .klotho directory: %v", err)
	}

	// Set up user if login is specified
	if cfg.login != "" {
		if err := analytics.CreateUser(cfg.login); err != nil {
			return errors.Wrapf(err, "could not configure user '%s'", cfg.login)
		}
		return nil
	}

	// Set up analytics
	analyticsClient, err := analytics.NewClient(map[string]interface{}{
		"version": km.Version,
		"strict":  cfg.strict,
		"edition": km.DefaultUpdateStream,
	})
	if err != nil {
		return errors.New(fmt.Sprintf("Issue retrieving user info: %s. \nYou may need to run: klotho --login <email>", err))
	}

	z, err := setupLogger(analyticsClient)
	if err != nil {
		return err
	}
	defer z.Sync() // nolint:errcheck
	zap.ReplaceGlobals(z)

	errHandler := ErrorHandler{
		InternalDebug: cfg.internalDebug,
		Verbose:       cfg.verbose,
	}
	defer analyticsClient.PanicHandler(&err, errHandler)

	// Save any config options. This should go before anything else, so that it always takes effect before any code
	// that uses it (for example, we should save an update.stream option before we use it below to perform the update).
	err = SetOptions(cfg.setOption)
	if err != nil {
		return err
	}
	options, err := ReadOptions()
	if err != nil {
		return err
	}
	updateStream := OptionOrDefault(options.Update.Stream, km.DefaultUpdateStream)
	analyticsClient.Properties["updateStream"] = updateStream

	if cfg.version {
		var versionQualifier string
		if km.VersionQualifier != "" {
			versionQualifier = fmt.Sprintf("(%s)", km.VersionQualifier)
		}
		zap.S().Infof("Version: %s-%s-%s%s", km.Version, updater.OS, updater.Arch, versionQualifier)
		return nil
	}
	klothoName := "klotho"
	if km.VersionQualifier != "" {
		klothoName += " " + km.VersionQualifier
	}

	// if update is specified do the update in place
	var klothoUpdater = updater.Updater{
		ServerURL:     updater.DefaultServer,
		Stream:        updateStream,
		CurrentStream: km.DefaultUpdateStream,
	}
	if cfg.update {
		if err := klothoUpdater.Update(km.Version); err != nil {
			analyticsClient.Error(klothoName + " failed to update")
			return err
		}
		analyticsClient.Info(klothoName + " was updated successfully")
		return nil
	}

	if ShouldCheckForUpdate(options.Update.Stream, km.DefaultUpdateStream, km.Version) {
		// check daily for new updates and notify users if found
		needsUpdate, err := klothoUpdater.CheckUpdate(km.Version)
		if err != nil {
			analyticsClient.Error(fmt.Sprintf(klothoName+"failed to check for updates: %v", err))
			zap.S().Warnf("failed to check for updates: %v", err)
		}
		if needsUpdate {
			analyticsClient.Info(klothoName + "update is available")
			zap.L().Info("new update is available, please run klotho --update to get the latest version")
		}
	} else {
		zap.S().Infof("Klotho is pinned to version: %s", options.Update.Stream)
	}

	if len(cfg.setOption) > 0 {
		// Options were set above, and used to perform or check for update. Nothing else to do.
		// We want to exit early, so that the user doesn't get an error about path not being provided.
		return nil
	}

	appCfg, err := readConfig(args)
	if err != nil {
		return errors.Wrapf(err, "could not read config '%s'", cfg.config)
	}

	if appCfg.Path == "" {
		return errors.New("'path' required")
	}

	if appCfg.AppName == "" {
		return errors.New("'app' required")
	} else if len(appCfg.AppName) > 35 {
		analyticsClient.Error("Klotho parameter check failed. 'app' must be less than 35 characters in length")
		return fmt.Errorf("'app' must be less than 35 characters in length. 'app' was %s", appCfg.AppName)
	}
	match, err := regexp.MatchString(`^[\w-.:/]+$`, appCfg.AppName)
	if err != nil {
		return err
	} else if !match {
		analyticsClient.Error("Klotho parameter check failed. 'app' can only contain alphanumeric, -, _, ., :, and /.")
		return fmt.Errorf("'app' can only contain alphanumeric, -, _, ., :, and /. 'app' was %s", appCfg.AppName)
	}

	if appCfg.Provider == "" {
		return errors.New("'provider' required")
	}

	// Update analytics with app configs
	analyticsClient.AppendProperties(map[string]interface{}{
		"provider": appCfg.Provider,
		"app":      appCfg.AppName,
	})

	analyticsClient.Info(klothoName + "pre-compile")

	input, err := input.ReadOSDir(appCfg, cfg.config)
	if err != nil {
		return errors.Wrapf(err, "could not read root path %s", appCfg.Path)
	}

	if cfg.ast {
		if err = OutputAST(input, appCfg.OutDir); err != nil {
			return errors.Wrap(err, "could not output helpers")
		}
	}
	if cfg.caps {
		if err = OutputCapabilities(input, appCfg.OutDir); err != nil {
			return errors.Wrap(err, "could not output helpers")
		}
	}

	plugins := &PluginSetBuilder{
		Cfg: &appCfg,
	}
	err = km.PluginSetup(plugins)

	if err != nil {
		return err
	}

	compiler := core.Compiler{
		Plugins: plugins.Plugins(),
	}

	analyticsClient.Info(klothoName + "compiling")

	result, err := compiler.Compile(input)
	if err != nil || hadErrors.Load() {
		if err != nil {
			errHandler.PrintErr(err)
		} else {
			err = errors.New("Failed run of klotho invocation")
		}
		analyticsClient.Error(klothoName + "compiling failed")

		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		return err
	}

	if cfg.uploadSource {
		analyticsClient.UploadSource(input)
	}

	resourceCounts, err := OutputResources(result, appCfg.OutDir)
	if err != nil {
		return err
	}

	CloseTreeSitter(result)
	analyticsClient.AppendProperties(map[string]interface{}{"resource_types": GetResourceTypeCount(result)})
	analyticsClient.AppendProperties(map[string]interface{}{"languages": GetLanguagesUsed(result)})
	analyticsClient.AppendProperties(map[string]interface{}{"resources": resourceCounts})
	analyticsClient.Info(klothoName + "compile complete")

	return nil
}
