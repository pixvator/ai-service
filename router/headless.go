package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetHeadlessRouter(router *gin.Engine) {
	setHeadlessAPIRouter(router)
	setHeadlessRelayRouter(router)
	setHeadlessVideoRouter(router)
	router.NoRoute(func(c *gin.Context) {
		controller.RelayNotFound(c)
	})
}

func setHeadlessAPIRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.BodyStorageCleanup())
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()

	apiRouter.GET("/status", controller.GetStatus)
	apiRouter.POST("/turnstile/verify", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.VerifyTurnstileJSON)
	apiRouter.POST("/verification/email", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.SendEmailVerificationJSON)
	apiRouter.GET("/oauth/:provider/login", middleware.CriticalRateLimit(), controller.HeadlessOAuthLogin)
	apiRouter.GET("/oauth/:provider/callback", middleware.CriticalRateLimit(), controller.HeadlessOAuthCallback)

	userRoute := apiRouter.Group("/user")
	{
		userRoute.POST("/register", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.HeadlessTurnstileCheck(), controller.HeadlessRegister)
		userRoute.GET("/email-exists", middleware.CriticalRateLimit(), controller.HeadlessCheckUserEmail)
	}

	tokenRoute := apiRouter.Group("/token")
	tokenRoute.Use(middleware.TokenAuth())
	{
		tokenRoute.GET("/", controller.GetAllTokens)
		tokenRoute.GET("/:id", controller.GetToken)
		tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
		tokenRoute.POST("/", controller.AddToken)
		tokenRoute.PUT("/", controller.UpdateToken)
		tokenRoute.DELETE("/:id", controller.DeleteToken)
	}

	usageRoute := apiRouter.Group("/usage")
	usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
	{
		tokenUsageRoute := usageRoute.Group("/token")
		tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
		tokenUsageRoute.GET("/", controller.GetTokenUsage)
	}

	apiRouter.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "ok"})
	})
}

func setHeadlessRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.BodyStorageCleanup())
	router.Use(middleware.StatsMiddleware())

	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.ListModels(c, constant.ChannelTypeAnthropic)
			case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "":
				controller.RetrieveModel(c, constant.ChannelTypeGemini)
			default:
				controller.ListModels(c, constant.ChannelTypeOpenAI)
			}
		})
		modelsRouter.GET("/:model", func(c *gin.Context) {
			if c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "" {
				controller.RetrieveModel(c, constant.ChannelTypeAnthropic)
				return
			}
			controller.RetrieveModel(c, constant.ChannelTypeOpenAI)
		})
	}

	geminiModelsRouter := router.Group("/v1beta/models")
	geminiModelsRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	geminiModelsRouter.GET("", func(c *gin.Context) {
		controller.ListModels(c, constant.ChannelTypeGemini)
	})

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	geminiCompatibleRouter.GET("", func(c *gin.Context) {
		controller.ListModels(c, constant.ChannelTypeOpenAI)
	})

	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.RouteTag("relay"))
	relayV1Router.Use(middleware.SystemPerformanceCheck())
	relayV1Router.Use(middleware.TokenAuth())
	relayV1Router.Use(middleware.ModelRequestRateLimit())
	{
		wsRouter := relayV1Router.Group("")
		wsRouter.Use(middleware.Distribute())
		wsRouter.GET("/realtime", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		httpRouter := relayV1Router.Group("")
		httpRouter.Use(middleware.Distribute())
		httpRouter.POST("/messages", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatClaude)
		})
		httpRouter.POST("/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/chat/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/responses", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponses)
		})
		httpRouter.POST("/responses/compact", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponsesCompaction)
		})
		httpRouter.POST("/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/generations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatEmbedding)
		})
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/rerank", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatRerank)
		})
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/moderations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/images/variations", controller.RelayNotImplemented)
		httpRouter.GET("/files", controller.RelayNotImplemented)
		httpRouter.POST("/files", controller.RelayNotImplemented)
		httpRouter.DELETE("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id/content", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes/:id/cancel", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id/events", controller.RelayNotImplemented)
		httpRouter.DELETE("/models/:model", controller.RelayNotImplemented)
	}

	relayMjRouter := router.Group("/mj")
	relayMjRouter.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjRouter)

	relayMjModeRouter := router.Group("/:mode/mj")
	relayMjModeRouter.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjModeRouter)

	relaySunoRouter := router.Group("/suno")
	relaySunoRouter.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(), middleware.Distribute())
	{
		relaySunoRouter.POST("/submit/:action", controller.RelayTask)
		relaySunoRouter.POST("/fetch", controller.RelayTaskFetch)
		relaySunoRouter.GET("/fetch/:id", controller.RelayTaskFetch)
	}

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(), middleware.ModelRequestRateLimit(), middleware.Distribute())
	relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
		controller.Relay(c, types.RelayFormatGemini)
	})
}

func setHeadlessVideoRouter(router *gin.Engine) {
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth())
	videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"), middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"), middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	jimengOfficialGroup.POST("/", controller.RelayTask)
}
