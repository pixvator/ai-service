package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func HeadlessOAuthLogin(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": i18n.T(c, i18n.MsgOAuthUnknownProvider)})
		return
	}
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}
	state := common.GetRandomString(24)
	redirectURL, err := headlessOAuthAuthURL(provider, providerName, state)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"state":        state,
			"redirect_url": redirectURL,
		},
	})
}

func HeadlessOAuthCallback(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": i18n.T(c, i18n.MsgOAuthUnknownProvider)})
		return
	}
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}
	if errCode := c.Query("error"); errCode != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": c.Query("error_description")})
		return
	}
	token, err := provider.ExchangeToken(c.Request.Context(), c.Query("code"), c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	user, err := findOrCreateHeadlessOAuthUser(provider, oauthUser, c.Query("aff"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgOAuthUserBanned)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
		},
	})
}

func findOrCreateHeadlessOAuthUser(provider oauth.Provider, oauthUser *oauth.OAuthUser, affCode string) (*model.User, error) {
	user := &model.User{}
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		if err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID); err != nil {
			return nil, err
		}
		if user.Id == 0 {
			return nil, &OAuthUserDeletedError{}
		}
		return user, nil
	}
	if legacyID, ok := oauthUser.Extra["legacy_id"].(string); ok && legacyID != "" && provider.IsUserIDTaken(legacyID) {
		if err := provider.FillUserByProviderID(user, legacyID); err != nil {
			return nil, err
		}
		if user.Id != 0 {
			return user, nil
		}
	}
	if !common.RegisterEnabled {
		return nil, &OAuthRegistrationDisabledError{}
	}

	user.Username = provider.GetProviderPrefix() + strconv.Itoa(model.GetMaxUserId()+1)
	if oauthUser.Username != "" {
		if exists, err := model.CheckUserExistOrDeleted(oauthUser.Username, ""); err == nil && !exists && len(oauthUser.Username) <= model.UserNameMaxLength {
			user.Username = oauthUser.Username
		}
	}
	if oauthUser.DisplayName != "" {
		user.DisplayName = oauthUser.DisplayName
	} else if oauthUser.Username != "" {
		user.DisplayName = oauthUser.Username
	} else {
		user.DisplayName = provider.GetName() + " User"
	}
	user.Email = oauthUser.Email
	user.Role = common.RoleCommonUser
	user.Status = common.UserStatusEnabled
	inviterID, _ := model.GetUserIdByAffCode(affCode)

	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := user.InsertWithTx(tx, inviterID); err != nil {
				return err
			}
			return model.CreateUserOAuthBindingWithTx(tx, &model.UserOAuthBinding{
				UserId:         user.Id,
				ProviderId:     genericProvider.GetProviderId(),
				ProviderUserId: oauthUser.ProviderUserID,
			})
		})
		if err != nil {
			return nil, err
		}
		user.FinalizeOAuthUserCreation(inviterID)
		return user, nil
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := user.InsertWithTx(tx, inviterID); err != nil {
			return err
		}
		provider.SetProviderUserID(user, oauthUser.ProviderUserID)
		return tx.Model(user).Updates(map[string]any{
			"github_id":   user.GitHubId,
			"discord_id":  user.DiscordId,
			"oidc_id":     user.OidcId,
			"linux_do_id": user.LinuxDOId,
			"wechat_id":   user.WeChatId,
			"telegram_id": user.TelegramId,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	user.FinalizeOAuthUserCreation(inviterID)
	return user, nil
}

func headlessOAuthAuthURL(provider oauth.Provider, providerName string, state string) (string, error) {
	values := url.Values{}
	values.Set("state", state)
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		config := genericProvider.GetConfig()
		values.Set("client_id", config.ClientId)
		values.Set("redirect_uri", fmt.Sprintf("%s/oauth/%s", system_setting.ServerAddress, config.Slug))
		values.Set("response_type", "code")
		values.Set("scope", config.Scopes)
		return config.AuthorizationEndpoint + "?" + values.Encode(), nil
	}
	switch providerName {
	case "github":
		values.Set("client_id", common.GitHubClientId)
		return "https://github.com/login/oauth/authorize?" + values.Encode(), nil
	case "discord":
		settings := system_setting.GetDiscordSettings()
		values.Set("client_id", settings.ClientId)
		values.Set("redirect_uri", fmt.Sprintf("%s/oauth/discord", system_setting.ServerAddress))
		values.Set("response_type", "code")
		values.Set("scope", "identify")
		return "https://discord.com/api/oauth2/authorize?" + values.Encode(), nil
	case "oidc":
		settings := system_setting.GetOIDCSettings()
		values.Set("client_id", settings.ClientId)
		values.Set("redirect_uri", fmt.Sprintf("%s/oauth/oidc", system_setting.ServerAddress))
		values.Set("response_type", "code")
		values.Set("scope", "openid email profile")
		return settings.AuthorizationEndpoint + "?" + values.Encode(), nil
	case "linuxdo":
		values.Set("client_id", common.LinuxDOClientId)
		values.Set("redirect_uri", fmt.Sprintf("%s/api/oauth/linuxdo", system_setting.ServerAddress))
		values.Set("response_type", "code")
		values.Set("scope", "read")
		authEndpoint := common.GetEnvOrDefaultString("LINUX_DO_AUTHORIZATION_ENDPOINT", "https://connect.linux.do/oauth2/authorize")
		return authEndpoint + "?" + values.Encode(), nil
	default:
		return "", fmt.Errorf("headless OAuth login URL is not available for provider %q", providerName)
	}
}
