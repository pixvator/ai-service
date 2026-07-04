package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	startTime := time.Now()

	if err := InitResources(); err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}
	defer func() {
		if err := model.CloseDB(); err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}()

	common.SysLog("Headless AI Billing Gateway " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	initRuntimeState()

	server := gin.New()
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v", err),
				"type":    "headless_gateway_panic",
			},
		})
	}))
	server.Use(middleware.RequestId())
	server.Use(middleware.PoweredBy())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	router.SetHeadlessRouter(server)

	port := os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: server,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.FatalLog("failed to start HTTP server: " + err.Error())
		}
	}()

	common.LogStartupSuccess(startTime, port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	common.SysLog(fmt.Sprintf("received signal: %v, shutting down...", sig))

	shutdownTimeout := time.Duration(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", 120)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		common.SysError(fmt.Sprintf("server forced to shutdown: %v", err))
	}
	common.SysLog("server exited")
}

func InitResources() error {
	err := godotenv.Load(".env")
	if err != nil && common.DebugEnabled {
		common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
	}

	common.InitEnv()
	common.IsMasterNode = false

	logger.SetupLogger()
	ratio_setting.InitRatioSettings()
	service.InitHttpClient()
	service.InitTokenEncoders()

	if err = model.InitDB(); err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}
	if err = authz.Init(model.DB); err != nil {
		common.FatalLog("failed to initialize authorization: " + err.Error())
		return err
	}

	model.InitOptionMap()
	model.GetPricing()

	if err = model.InitLogDB(); err != nil {
		return err
	}
	if err = common.InitRedisClient(); err != nil {
		return err
	}

	if err = i18n.Init(); err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
	} else {
		common.SysLog("i18n initialized with languages: " + fmt.Sprintf("%v", i18n.SupportedLanguages()))
	}
	i18n.SetUserLangLoader(model.GetUserLanguage)

	if err = oauth.LoadCustomProviders(); err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
	}
	return nil
}

func initRuntimeState() {
	if common.RedisEnabled {
		common.MemoryCacheEnabled = true
	}
	if !common.MemoryCacheEnabled {
		return
	}
	common.SysLog("memory cache enabled")
	defer func() {
		if r := recover(); r != nil {
			common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
			if _, _, err := model.FixAbility(); err != nil {
				common.FatalLog(fmt.Sprintf("InitChannelCache failed: %s", err.Error()))
			}
		}
	}()
	model.InitChannelCache()
}
