package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

type headlessTurnstilePayload struct {
	TurnstileToken string `json:"turnstile_token"`
}

type headlessTurnstileResponse struct {
	Success bool `json:"success"`
}

func HeadlessTurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.TurnstileCheckEnabled {
			c.Next()
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		var payload headlessTurnstilePayload
		if err := common.Unmarshal(body, &payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid json"})
			c.Abort()
			return
		}
		if err := verifyHeadlessTurnstile(c, payload.TurnstileToken); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			c.Abort()
			return
		}
		c.Next()
	}
}

func SendEmailVerificationJSON(c *gin.Context) {
	var payload struct {
		Email          string `json:"email"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := verifyHeadlessTurnstile(c, payload.TurnstileToken); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	email := strings.TrimSpace(payload.Email)
	if err := common.Validate.Var(email, "required,email"); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid email"})
		return
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid email"})
		return
	}
	localPart, domainPart := parts[0], parts[1]
	if common.EmailDomainRestrictionEnabled {
		allowed := false
		for _, domain := range common.EmailDomainWhitelist {
			if domainPart == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "email domain is not allowed"})
			return
		}
	}
	if common.EmailAliasRestrictionEnabled && strings.ContainsAny(localPart, "+.") {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "email alias is not allowed"})
		return
	}
	if model.IsEmailAlreadyTaken(email) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "email already taken"})
		return
	}

	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	subject := fmt.Sprintf("%s邮箱验证邮件", common.SystemName)
	content := fmt.Sprintf("<p>您好，你正在进行%s邮箱验证。</p>"+
		"<p>您的验证码为: <strong>%s</strong></p>"+
		"<p>验证码 %d 分钟内有效，如果不是本人操作，请忽略。</p>", common.SystemName, code, common.VerificationValidMinutes)
	if err := common.SendEmail(subject, email, content); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func HeadlessRegister(c *gin.Context) {
	if !common.RegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		return
	}
	if !common.PasswordRegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordRegisterDisabled)
		return
	}

	var user model.User
	if err := common.DecodeJson(c.Request.Body, &user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user.Username = strings.TrimSpace(user.Username)
	user.Email = strings.TrimSpace(user.Email)
	if user.Username == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	if common.EmailVerificationEnabled {
		if user.Email == "" || user.VerificationCode == "" {
			common.ApiErrorI18n(c, i18n.MsgUserEmailVerificationRequired)
			return
		}
		if !common.VerifyCodeWithKey(user.Email, user.VerificationCode, common.EmailVerificationPurpose) {
			common.ApiErrorI18n(c, i18n.MsgUserVerificationCodeError)
			return
		}
	}
	exist, err := model.CheckUserExistOrDeleted(user.Username, user.Email)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		common.SysLog(fmt.Sprintf("CheckUserExistOrDeleted error: %v", err))
		return
	}
	if exist {
		common.ApiErrorI18n(c, i18n.MsgUserExists)
		return
	}

	inviterID, _ := model.GetUserIdByAffCode(user.AffCode)
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.Username,
		InviterId:   inviterID,
		Role:        common.RoleCommonUser,
	}
	if common.EmailVerificationEnabled {
		cleanUser.Email = user.Email
	}
	if err := cleanUser.Insert(inviterID); err != nil {
		common.ApiError(c, err)
		return
	}

	if constant.GenerateDefaultToken {
		insertedUser := model.User{}
		if err := model.DB.Where("username = ?", cleanUser.Username).First(&insertedUser).Error; err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserRegisterFailed)
			return
		}
		key, err := common.GenerateKey()
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserDefaultTokenFailed)
			common.SysLog("failed to generate token key: " + err.Error())
			return
		}
		token := model.Token{
			UserId:             insertedUser.Id,
			Name:               cleanUser.Username + "的初始令牌",
			Key:                key,
			CreatedTime:        common.GetTimestamp(),
			AccessedTime:       common.GetTimestamp(),
			ExpiredTime:        -1,
			RemainQuota:        500000,
			UnlimitedQuota:     true,
			ModelLimitsEnabled: false,
		}
		if setting.DefaultUseAutoGroup {
			token.Group = "auto"
		}
		if err := token.Insert(); err != nil {
			common.ApiErrorI18n(c, i18n.MsgCreateDefaultTokenErr)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func verifyHeadlessTurnstile(c *gin.Context, token string) error {
	if !common.TurnstileCheckEnabled {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("turnstile token is required")
	}
	rawRes, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
		"secret":   {common.TurnstileSecretKey},
		"response": {token},
		"remoteip": {c.ClientIP()},
	})
	if err != nil {
		return err
	}
	defer rawRes.Body.Close()
	var res headlessTurnstileResponse
	if err := common.DecodeJson(rawRes.Body, &res); err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("turnstile verification failed")
	}
	return nil
}
