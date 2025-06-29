// internal/controllers/app.go
package controllers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/tyottodekiru/k8s-playground/pkg/k8s"
	"github.com/tyottodekiru/k8s-playground/pkg/queue"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	writeWait          = 10 * time.Second
	pongWait           = 60 * time.Second
	pingPeriod         = (pongWait * 9) / 10
	maxMessageSize     = 8192
	sessionName        = "k8s-playground-session"
	legacyOwnerID      = "legacy_admin_user"
)

type TerminalMessage struct {
	Operation string `json:"operation"`
	Data      string `json:"data"`
	Rows      uint16 `json:"rows"`
	Cols      uint16 `json:"cols"`
}

type TerminalSession struct {
	id       string
	bound    chan error
	sizeChan chan *k8s.TerminalSize
	doneChan chan struct{}
}

func NewTerminalSession(sessionId string) *TerminalSession {
	return &TerminalSession{
		id:       sessionId,
		bound:    make(chan error),
		sizeChan: make(chan *k8s.TerminalSize, 2),
		doneChan: make(chan struct{}),
	}
}
func (t *TerminalSession) Done() <-chan struct{} { return t.doneChan }
func (t *TerminalSession) Close()                { close(t.doneChan) }
func (t *TerminalSession) Next() *k8s.TerminalSize {
	select {
	case size := <-t.sizeChan:
		return size
	case <-t.doneChan:
		return nil
	}
}
func (t *TerminalSession) Resize(cols, rows uint16) {
	select {
	case t.sizeChan <- &k8s.TerminalSize{Width: cols, Height: rows}:
	case <-time.After(100 * time.Millisecond):
		select {
		case <-t.sizeChan:
			select {
			case t.sizeChan <- &k8s.TerminalSize{Width: cols, Height: rows}:
			default:
			}
		default:
		}
	}
}

type WSClient struct {
	conn    *websocket.Conn
	session *TerminalSession
	mutex   sync.Mutex
	// Logging fields
	environmentID string
	userID        string
	userName      string
	podName       string
	sessionID     string
	logger        *LoggingController
}

func NewWSClient(conn *websocket.Conn, session *TerminalSession) *WSClient {
	client := &WSClient{conn: conn, session: session}
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	go client.startPingTimer()
	return client
}

func NewWSClientWithLogging(conn *websocket.Conn, session *TerminalSession, environmentID, userID, userName, podName, sessionID string, logger *LoggingController) *WSClient {
	client := &WSClient{
		conn:          conn,
		session:       session,
		environmentID: environmentID,
		userID:        userID,
		userName:      userName,
		podName:       podName,
		sessionID:     sessionID,
		logger:        logger,
	}
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	go client.startPingTimer()
	return client
}
func (c *WSClient) Read(p []byte) (n int, err error) {
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		}
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return 0, fmt.Errorf("websocket closed: %w", err)
			}
			return 0, err
		}
		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			var controlMsg map[string]interface{}
			if errJSON := json.Unmarshal(message, &controlMsg); errJSON == nil {
				if resize, ok := controlMsg["resize"].(bool); ok && resize {
					if cols, okCols := controlMsg["cols"].(float64); okCols {
						if rows, okRows := controlMsg["rows"].(float64); okRows {
							c.session.Resize(uint16(cols), uint16(rows))
							continue
						}
					}
				}
			}

			// Log command if logger is available
			if c.logger != nil && c.environmentID != "" && c.userID != "" {
				if command := c.logger.ParseCommandFromWebSocketDataWithSession(message, c.sessionID); command != "" {
					go func() {
						if err := c.logger.LogCommandToBuffer(c.environmentID, c.userID, c.userName, c.podName, command, c.sessionID); err != nil {
							log.Printf("Failed to buffer command: %v", err)
							// Fallback to direct logging
							if err := c.logger.LogCommand(c.environmentID, c.userID, c.userName, c.podName, command, c.sessionID); err != nil {
								log.Printf("Failed to log command directly: %v", err)
							}
						}
					}()
				}
			}

			n = copy(p, message)
			return n, nil
		}
	}
}
func (c *WSClient) Write(p []byte) (n int, err error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
	}
	const maxChunkSize = 4096
	totalWritten := 0
	for len(p) > 0 {
		chunkSize := len(p)
		if chunkSize > maxChunkSize {
			chunkSize = maxChunkSize
		}
		chunk := p[:chunkSize]
		if err := c.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return totalWritten, err
		}
		totalWritten += chunkSize
		p = p[chunkSize:]
		if len(p) > 0 {
			time.Sleep(1 * time.Millisecond)
		}
	}
	return totalWritten, nil
}
func (c *WSClient) startPingTimer() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mutex.Lock()
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.mutex.Unlock()
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.mutex.Unlock()
				return
			}
			c.mutex.Unlock()
		case <-c.session.Done():
			return
		}
	}
}
func (c *WSClient) Close() error { c.session.Close(); return c.conn.Close() }

type AppController struct {
	redisQueue              *queue.RedisQueue
	k8sClient               *k8s.Client
	upgrader                websocket.Upgrader
	oauth2Config            *oauth2.Config
	sessionStore            sessions.Store
	authMethod              string
	legacyAuthPassword      string
	googleAllowedDomains    []string
	dindImageVersions       map[string]string
	dindWorkloadType        string // ★ フィールドを追加
	loggingController       *LoggingController
	loggingControllerAPIURL string
	loggingAdminToken       string
}

func NewAppController(
	redisQueue *queue.RedisQueue,
	oauth2Config *oauth2.Config,
	store sessions.Store,
	authMethod string,
	legacyAuthPassword string,
	googleAllowedDomains []string,
	dindImageVersions map[string]string,
	dindWorkloadType string, // ★ 引数を追加
	loggingControllerAPIURL string,
	loggingAdminToken string,
) *AppController {
	k8sClient, err := k8s.NewClient()
	if err != nil {
		log.Printf("Warning: Failed to initialize k8s client: %v. Some functionalities might be affected.", err)
	}

	// Initialize logging controller with Redis buffering
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = "/var/log/k8s-playground"
	}

	return &AppController{
		redisQueue:              redisQueue,
		k8sClient:               k8sClient,
		oauth2Config:            oauth2Config,
		sessionStore:            store,
		authMethod:              authMethod,
		legacyAuthPassword:      legacyAuthPassword,
		googleAllowedDomains:    googleAllowedDomains,
		dindImageVersions:       dindImageVersions,
		dindWorkloadType:        dindWorkloadType, // ★ 初期化
		loggingController:       NewLoggingControllerWithRedis(logDir, redisQueue.Client),
		loggingControllerAPIURL: loggingControllerAPIURL,
		loggingAdminToken:       loggingAdminToken,
		upgrader: websocket.Upgrader{
			CheckOrigin:  func(r *http.Request) bool { return true },
			Subprotocols: []string{"base64.channel.k8s.io"},
		},
	}
}

func (a *AppController) SetupRoutes(router *gin.Engine) {
	router.Static("/static", "./web/static")
	router.LoadHTMLGlob("web/templates/*")

	router.GET("/", a.loginPage)
	router.GET("/logout", a.handleLogout)

	if a.authMethod == "google" {
		router.GET("/login/google", a.handleGoogleLogin)
		router.GET("/auth/google/callback", a.handleGoogleCallback)
	} else if a.authMethod == "password" {
		router.POST("/login", a.handleLegacyLogin)
	}

	authGroup := router.Group("/")
	authGroup.Use(a.authMiddleware())
	{
		authGroup.GET("/dashboard", a.dashboard)
		authGroup.GET("/api/environments", a.getEnvironments)
		authGroup.POST("/api/environments", a.createEnvironment)
		authGroup.DELETE("/api/environments/:id", a.destroyEnvironment)
		authGroup.PUT("/api/environments/:id/displayname", a.updateEnvironmentDisplayName)
		authGroup.GET("/api/environments/:id/connect", a.connectEnvironment)
		authGroup.GET("/api/environments/:id/services", a.getEnvironmentServices)
		authGroup.Any("/api/environments/:id/browser/*path", a.proxyToPod)
		authGroup.GET("/api/user", a.getUserInfo)
		authGroup.GET("/api/k8s-versions", a.getAvailableK8sVersions)
	}

	// Admin routes for logging
	adminGroup := router.Group("/admin")
	adminGroup.Use(a.authMiddleware())
	adminGroup.Use(a.adminMiddleware())
	{
		adminGroup.GET("/", a.adminDashboard)
		adminGroup.GET("/api/command-logs", a.getCommandLogs)
		adminGroup.GET("/api/all-environments", a.getAllEnvironments)
	}
}

func (a *AppController) getAvailableK8sVersions(c *gin.Context) {
	log.Printf("getAvailableK8sVersions called. dindImageVersions: %+v", a.dindImageVersions)
	versions := make([]string, 0, len(a.dindImageVersions))
	for k := range a.dindImageVersions {
		versions = append(versions, k)
	}
	sort.Strings(versions)
	log.Printf("Returning versions: %+v", versions)
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (a *AppController) getUserInfo(c *gin.Context) {
	ownerID, exists := c.Get("owner_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}
	displayName := ownerID.(string)
	if a.authMethod == "password" && ownerID == legacyOwnerID {
		displayName = "Admin (Password Auth)"
	} else {
		name, _ := c.Get("user_name")
		if nameStr, ok := name.(string); ok && nameStr != "" {
			displayName = nameStr
		}
	}
	userPicture, _ := c.Get("user_picture")
	c.JSON(http.StatusOK, gin.H{
		"owner_id": ownerID, "display_name": displayName, "picture": userPicture, "auth_method": a.authMethod,
	})
}

func (a *AppController) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		session, err := a.sessionStore.Get(c.Request, sessionName)
		if err != nil {
			log.Printf("Session store error in authMiddleware: %v", err)
			c.Redirect(http.StatusFound, "/")
			c.Abort()
			return
		}
		auth, okAuth := session.Values["authenticated"].(bool)
		if !okAuth || !auth {
			if c.Request.Header.Get("Upgrade") == "websocket" {
				log.Printf("WebSocket authentication failed for request to %s", c.Request.URL.Path)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized WebSocket connection"})
			} else {
				c.Redirect(http.StatusFound, "/")
			}
			c.Abort()
			return
		}
		var ownerID string
		if a.authMethod == "google" {
			userEmail, okEmail := session.Values["user_email"].(string)
			if !okEmail || userEmail == "" {
				log.Printf("User email not found in session for authenticated Google user.")
				session.Values["authenticated"] = false
				session.Options.MaxAge = -1
				if err := session.Save(c.Request, c.Writer); err != nil {
					log.Printf("Error saving session during auth failure: %v", err)
				}
				if c.Request.Header.Get("Upgrade") == "websocket" {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "User email missing in session"})
				} else {
					c.Redirect(http.StatusFound, "/")
				}
				c.Abort()
				return
			}
			ownerID = userEmail
			c.Set("user_email", userEmail)
			if name, ok := session.Values["user_name"].(string); ok {
				c.Set("user_name", name)
			}
			if picture, ok := session.Values["user_picture"].(string); ok {
				c.Set("user_picture", picture)
			}
		} else if a.authMethod == "password" {
			userID, okUserID := session.Values["user_id"].(string)
			if !okUserID || userID != legacyOwnerID {
				log.Printf("Invalid user_id in session for password auth.")
				session.Values["authenticated"] = false
				session.Options.MaxAge = -1
				if err := session.Save(c.Request, c.Writer); err != nil {
					log.Printf("Error saving session during auth failure: %v", err)
				}
				if c.Request.Header.Get("Upgrade") == "websocket" {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid session for password auth"})
				} else {
					c.Redirect(http.StatusFound, "/")
				}
				c.Abort()
				return
			}
			ownerID = legacyOwnerID
		} else {
			log.Printf("Unknown auth method in authMiddleware: %s", a.authMethod)
			if c.Request.Header.Get("Upgrade") == "websocket" {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			} else {
				c.Redirect(http.StatusFound, "/?error=config_error")
			}
			c.Abort()
			return
		}
		c.Set("owner_id", ownerID)
		c.Next()
	}
}

func (a *AppController) loginPage(c *gin.Context) {
	session, _ := a.sessionStore.Get(c.Request, sessionName)
	if auth, ok := session.Values["authenticated"].(bool); ok && auth {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	c.HTML(http.StatusOK, "login.html", gin.H{"title": "k8s Playground - Login", "AuthMethod": a.authMethod, "error": c.Query("error")})
}

func (a *AppController) handleLegacyLogin(c *gin.Context) {
	if a.authMethod != "password" {
		c.HTML(http.StatusForbidden, "login.html", gin.H{"title": "Login Error", "error": "Password login is not enabled.", "AuthMethod": a.authMethod})
		return
	}
	password := c.PostForm("password")
	if password != a.legacyAuthPassword {
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{"title": "k8s Playground - Login", "error": "Invalid password", "AuthMethod": a.authMethod})
		return
	}
	session, _ := a.sessionStore.Get(c.Request, sessionName)
	session.Values["authenticated"] = true
	session.Values["user_id"] = legacyOwnerID
	if err := session.Save(c.Request, c.Writer); err != nil {
		log.Printf("Error saving session in handleLegacyLogin: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Failed to save session.", "AuthMethod": a.authMethod})
		return
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

func (a *AppController) handleGoogleLogin(c *gin.Context) {
	if a.authMethod != "google" || a.oauth2Config == nil {
		c.HTML(http.StatusForbidden, "login.html", gin.H{"title": "Login Error", "error": "Google login is not enabled or configured.", "AuthMethod": a.authMethod})
		return
	}
	session, err := a.sessionStore.Get(c.Request, sessionName)
	if err != nil {
		log.Printf("Session store error in handleGoogleLogin: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Session error, please try again.", "AuthMethod": a.authMethod})
		return
	}
	oauthState := make([]byte, 16)
	if _, err := rand.Read(oauthState); err != nil {
		log.Printf("Error generating oauth state: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Could not initiate login, please try again.", "AuthMethod": a.authMethod})
		return
	}
	stateString := base64.URLEncoding.EncodeToString(oauthState)
	session.Values["oauth_state"] = stateString
	if err := session.Save(c.Request, c.Writer); err != nil {
		log.Printf("Error saving session in handleGoogleLogin: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Could not save session, please try again.", "AuthMethod": a.authMethod})
		return
	}
	var url string
	authCodeURLOptions := []oauth2.AuthCodeOption{oauth2.AccessTypeOnline}
	if len(a.googleAllowedDomains) == 1 && a.googleAllowedDomains[0] != "" {
		authCodeURLOptions = append(authCodeURLOptions, oauth2.SetAuthURLParam("hd", a.googleAllowedDomains[0]))
	}
	url = a.oauth2Config.AuthCodeURL(stateString, authCodeURLOptions...)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (a *AppController) handleGoogleCallback(c *gin.Context) {
	if a.authMethod != "google" || a.oauth2Config == nil {
		c.HTML(http.StatusForbidden, "login.html", gin.H{"title": "Login Error", "error": "Google login is not enabled or configured.", "AuthMethod": a.authMethod})
		return
	}
	session, err := a.sessionStore.Get(c.Request, sessionName)
	if err != nil {
		log.Printf("Session store error in handleGoogleCallback: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Session error during callback, please try again.", "AuthMethod": a.authMethod})
		return
	}
	sessionState, ok := session.Values["oauth_state"].(string)
	if !ok || sessionState == "" || c.Query("state") != sessionState {
		log.Printf("Invalid oauth state. Session: '%s', Query: '%s'", sessionState, c.Query("state"))
		c.HTML(http.StatusBadRequest, "login.html", gin.H{"title": "Login Error", "error": "Invalid session state. Please try logging in again.", "AuthMethod": a.authMethod})
		return
	}
	code := c.Query("code")
	token, err := a.oauth2Config.Exchange(context.Background(), code)
	if err != nil {
		log.Printf("Failed to exchange token: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Failed to exchange token: " + err.Error(), "AuthMethod": a.authMethod})
		return
	}
	idTokenString, ok := token.Extra("id_token").(string)
	if !ok {
		log.Printf("ID token not found in token response")
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Could not get ID token from Google.", "AuthMethod": a.authMethod})
		return
	}
	payload, err := idtoken.Validate(context.Background(), idTokenString, a.oauth2Config.ClientID)
	if err != nil {
		log.Printf("Failed to validate ID token: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Failed to validate ID token: " + err.Error(), "AuthMethod": a.authMethod})
		return
	}
	if len(a.googleAllowedDomains) > 0 && !(len(a.googleAllowedDomains) == 1 && a.googleAllowedDomains[0] == "") {
		hdClaim, claimOk := payload.Claims["hd"].(string)
		isAllowed := false
		if claimOk {
			for _, allowedDomain := range a.googleAllowedDomains {
				if hdClaim == allowedDomain {
					isAllowed = true
					break
				}
			}
		}
		if !isAllowed {
			log.Printf("User from different domain tried to login. Expected one of: %v, Got: %s (Claim OK: %v)", a.googleAllowedDomains, hdClaim, claimOk)
			errorMsg := fmt.Sprintf("You must log in with an account from one of the allowed domains: %v.", a.googleAllowedDomains)
			if len(a.googleAllowedDomains) == 1 {
				errorMsg = "You must log in with an account from the " + a.googleAllowedDomains[0] + " domain."
			}
			c.HTML(http.StatusForbidden, "login.html", gin.H{"title": "Login Error", "error": errorMsg, "AuthMethod": a.authMethod})
			return
		}
	}
	userEmail := payload.Claims["email"].(string)
	userName, _ := payload.Claims["name"].(string)
	userPicture, _ := payload.Claims["picture"].(string)
	if userEmail == "" {
		log.Printf("User email not provided by Google (from ID token).")
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "User email not provided by Google.", "AuthMethod": a.authMethod})
		return
	}
	session.Values["user_email"] = userEmail
	session.Values["user_name"] = userName
	session.Values["user_picture"] = userPicture
	session.Values["authenticated"] = true
	delete(session.Values, "oauth_state")
	if err := session.Save(c.Request, c.Writer); err != nil {
		log.Printf("Error saving session in handleGoogleCallback: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"title": "Login Error", "error": "Failed to save session after login.", "AuthMethod": a.authMethod})
		return
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

func (a *AppController) handleLogout(c *gin.Context) {
	session, err := a.sessionStore.Get(c.Request, sessionName)
	if err == nil {
		session.Values["authenticated"] = false
		delete(session.Values, "user_email")
		delete(session.Values, "user_name")
		delete(session.Values, "user_picture")
		delete(session.Values, "oauth_state")
		delete(session.Values, "user_id")
		session.Options.MaxAge = -1
		if err := session.Save(c.Request, c.Writer); err != nil {
			log.Printf("Error saving session during logout: %v", err)
		}
	} else {
		log.Printf("Session store error in handleLogout: %v", err)
	}
	c.Redirect(http.StatusFound, "/")
}

func (a *AppController) dashboard(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	displayName := ownerID
	userPicture := ""
	if a.authMethod == "google" {
		name, okName := c.Get("user_name")
		pic, okPic := c.Get("user_picture")
		if okName {
			displayName = name.(string)
		}
		if okPic {
			userPicture = pic.(string)
		}
	} else if a.authMethod == "password" {
		displayName = "Admin (Password Auth)"
	}
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title": "k8s Playground - Dashboard", "OwnerID": ownerID, "DisplayName": displayName, "UserPicture": userPicture, "AuthMethod": a.authMethod,
	})
}

func (a *AppController) getEnvironments(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	ctx := context.Background()
	environments, err := a.redisQueue.GetItemsByOwner(ctx, ownerID)
	if err != nil {
		log.Printf("Error getting environments for owner %s: %v", ownerID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get environments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"environments": environments})
}

func (a *AppController) createEnvironment(c *gin.Context) {
	var req struct {
		K8sVersion  string `json:"k8s_version"`
		DisplayName string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if req.K8sVersion == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "k8s_version is required"})
		return
	}
	if len(req.DisplayName) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "DisplayName cannot exceed 50 characters"})
		return
	}
	ownerID := c.MustGet("owner_id").(string)

	// ★ WorkloadType を設定
	workloadType := a.dindWorkloadType
	if workloadType != "statefulset" && workloadType != "deployment" {
		workloadType = "statefulset" // 安全のためのフォールバック
	}

	item := &queue.QueueItem{
		Owner:           ownerID,
		K8sVersion:      req.K8sVersion,
		DisplayName:     req.DisplayName,
		Status:          queue.StatusPending,
		StatusUpdatedAt: time.Now(),
		ExpiresAt:       time.Now().Add(24 * time.Hour),
		WorkloadType:    workloadType, // ★ WorkloadTypeをセット
	}
	ctx := context.Background()
	if err := a.redisQueue.AddItem(ctx, item); err != nil {
		log.Printf("Error creating environment for owner %s (version %s, name %s): %v", ownerID, req.K8sVersion, req.DisplayName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create environment"})
		return
	}
	log.Printf("Environment created: ID %s, Owner %s, Version %s, Name %s, Type %s", item.ID, item.Owner, item.K8sVersion, item.DisplayName, item.WorkloadType)
	c.JSON(http.StatusCreated, gin.H{"environment": item})
}

func (a *AppController) updateEnvironmentDisplayName(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	envID := c.Param("id")
	var req struct {
		DisplayName string `json:"display_name" binding:"required,max=50"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	ctx := context.Background()
	item, err := a.redisQueue.GetItem(ctx, envID)
	if err != nil {
		if err.Error() == "item not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		} else {
			log.Printf("Error getting environment %s for name update by owner %s: %v", envID, ownerID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve environment details"})
		}
		return
	}
	if item.Owner != ownerID {
		log.Printf("Forbidden: Owner %s attempted to update name for environment %s owned by %s", ownerID, envID, item.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the owner of this environment"})
		return
	}
	item.DisplayName = req.DisplayName
	item.StatusUpdatedAt = time.Now()
	if err := a.redisQueue.UpdateItem(ctx, item); err != nil {
		log.Printf("Error updating display name for environment %s by owner %s: %v", envID, ownerID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update environment display name"})
		return
	}
	log.Printf("Environment display name updated: ID %s, New Name '%s', Owner %s", item.ID, item.DisplayName, item.Owner)
	c.JSON(http.StatusOK, gin.H{"environment": item})
}

func (a *AppController) destroyEnvironment(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	id := c.Param("id")
	ctx := context.Background()
	item, err := a.redisQueue.GetItem(ctx, id)
	if err != nil {
		if err.Error() == "item not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		} else {
			log.Printf("Error getting environment %s for owner %s: %v", id, ownerID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve environment details"})
		}
		return
	}
	if item.Owner != ownerID {
		log.Printf("Forbidden: Owner %s attempted to destroy environment %s owned by %s", ownerID, id, item.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the owner of this environment"})
		return
	}
	item.Status = queue.StatusShutdown
	item.StatusUpdatedAt = time.Now()
	if err := a.redisQueue.UpdateItem(ctx, item); err != nil {
		log.Printf("Error marking environment %s for destruction by owner %s: %v", id, ownerID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to destroy environment"})
		return
	}
	log.Printf("Environment %s marked for destruction by owner %s", id, ownerID)
	c.JSON(http.StatusOK, gin.H{"message": "Environment marked for destruction"})
}

func (a *AppController) connectEnvironment(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	envId := c.Param("id")
	ctx := context.Background()
	item, err := a.redisQueue.GetItem(ctx, envId)
	if err != nil {
		log.Printf("Connect: Environment %s not found for owner %s. Error: %v", envId, ownerID, err)
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		return
	}
	if item.Owner != ownerID {
		log.Printf("Connect: Owner %s attempted to access environment %s owned by %s.", ownerID, envId, item.Owner)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You are not the owner of this environment"})
		return
	}
	if item.Status != queue.StatusAvailable {
		log.Printf("Connect: Environment %s not available for owner %s. Status: %s", envId, ownerID, item.Status)
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Environment not available"})
		return
	}
	if a.k8sClient == nil {
		log.Printf("Connect: Kubernetes client not available for environment %s, owner %s.", envId, ownerID)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Kubernetes client not available"})
		return
	}
	if item.PodID == "" {
		log.Printf("Connect: Pod ID (StatefulSet name) not available for environment %s, owner %s.", envId, ownerID)
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Pod ID not available"})
		return
	}

	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	var podName string
	var errGetPod error

	// ★ ワークロードタイプに応じてPod名を取得する方法を分岐
	if item.WorkloadType == "deployment" {
		podName, errGetPod = a.k8sClient.GetPodNameForWorkload(c.Request.Context(), item.PodID, namespace)
	} else {
		podName = fmt.Sprintf("%s-0", item.PodID)
	}

	if errGetPod != nil {
		log.Printf("Connect: Failed to get pod name for workload %s (env %s): %v", item.PodID, envId, errGetPod)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Could not find the running pod for the environment"})
		return
	}

	log.Printf("Attempting to connect to pod %s for workload %s (env %s)", podName, item.PodID, envId)

	conn, err := a.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade WebSocket connection for env %s, owner %s: %v", envId, ownerID, err)
		return
	}
	log.Printf("WebSocket connection upgraded for env %s, owner %s", envId, ownerID)
	// ★ handleTerminalSessionにpodNameとnamespaceを渡すように変更
	a.handleTerminalSession(conn, item, podName, namespace)
}

// ★ handleTerminalSessionのシグネチャを変更
func (a *AppController) handleTerminalSession(conn *websocket.Conn, item *queue.QueueItem, podName string, namespace string) {
	defer func() {
		log.Printf("Closing WebSocket for session to pod %s (env %s)", podName, item.ID)
		conn.Close()
	}()

	running, err := a.k8sClient.IsPodRunning(context.Background(), podName, namespace)
	if err != nil {
		log.Printf("Error re-checking pod status for %s: %v", podName, err)
		a.sendErrorMessage(conn, fmt.Sprintf("Error checking pod status: %v", err))
		return
	}
	if !running {
		log.Printf("Pod %s is no longer running before exec", podName)
		a.sendErrorMessage(conn, "Pod is not running")
		return
	}

	sessionId := fmt.Sprintf("%s-%s-%d", item.Owner, podName, time.Now().UnixNano())
	session := NewTerminalSession(sessionId)
	defer session.Close()

	// Get user information for logging
	ownerID := item.Owner
	userName := ownerID // Default to owner ID
	
	// Create WSClient with logging capability
	wsClient := NewWSClientWithLogging(conn, session, item.ID, ownerID, userName, podName, sessionId, a.loggingController)

	_, initialMessage, err := conn.ReadMessage()
	if err != nil {
		log.Printf("Failed to read initial message for session %s: %v", sessionId, err)
		return
	}
	var initMsg struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if err := json.Unmarshal(initialMessage, &initMsg); err != nil {
		log.Printf("Could not parse initial JSON from client for session %s: %v. Using defaults.", sessionId, err)
		initMsg.Cols = 80
		initMsg.Rows = 24
	}
	if initMsg.Cols > 0 && initMsg.Rows > 0 {
		session.Resize(uint16(initMsg.Cols), uint16(initMsg.Rows))
	} else {
		session.Resize(80, 24)
	}
	displayName := item.DisplayName
	if displayName == "" {
		displayName = item.ID[:8]
	}
	a.sendRawMessage(conn, fmt.Sprintf("\x1b[32mWelcome! Connecting to your Kubernetes environment '%s' (Pod: %s)...\x1b[0m\r\n", displayName, podName))

	containerName := "dind"
	command := []string{"/bin/bash"}
	execCtx, cancelExec := context.WithCancel(context.Background())
	defer cancelExec()

	go func() {
		defer cancelExec()
		log.Printf("Starting exec for session %s in pod %s", sessionId, podName)
		err := a.k8sClient.ExecInPod(execCtx, namespace, podName, containerName, command, wsClient, wsClient, wsClient, session)
		if err != nil {
			errMsg := fmt.Sprintf("Terminal session error: %v", err)
			log.Printf("Exec error for session %s: %v", sessionId, err)
			if conn.UnderlyingConn() != nil {
				a.sendErrorMessage(conn, errMsg)
			}
		}
		log.Printf("Exec finished for session %s", sessionId)
	}()

	select {
	case <-session.Done():
		log.Printf("Terminal session %s marked as done.", sessionId)
	case <-execCtx.Done():
		log.Printf("Exec context for session %s done: %v", sessionId, execCtx.Err())
	}
	log.Printf("Exiting handleTerminalSession for session %s", sessionId)
}

func (a *AppController) sendErrorMessage(conn *websocket.Conn, message string) {
	msg := TerminalMessage{Operation: "error", Data: "\x1b[31m" + message + "\x1b[0m\r\n"}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshalling error message to JSON: %v", err)
		if rawErr := conn.WriteMessage(websocket.TextMessage, []byte(message+"\r\n")); rawErr != nil {
			log.Printf("Error sending raw error message to WebSocket: %v", rawErr)
		}
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
		log.Printf("Error sending JSON error message to WebSocket: %v", err)
	}
}
func (a *AppController) sendRawMessage(conn *websocket.Conn, message string) {
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		log.Printf("Error sending raw message to WebSocket: %v", err)
	}
}
func (a *AppController) sendStatusMessage(conn *websocket.Conn, message string) {
	msg := TerminalMessage{Operation: "status", Data: message + "\r\n"}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshalling status message to JSON: %v", err)
		if rawErr := conn.WriteMessage(websocket.TextMessage, []byte(message+"\r\n")); rawErr != nil {
			log.Printf("Error sending raw status message to WebSocket: %v", rawErr)
		}
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
		log.Printf("Error sending JSON status message to WebSocket: %v", err)
	}
}

// adminMiddleware checks if the user is an administrator
func (a *AppController) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ownerID, exists := c.Get("owner_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			c.Abort()
			return
		}

		// For password auth, the legacy admin user is always admin
		if a.authMethod == "password" && ownerID == legacyOwnerID {
			c.Next()
			return
		}

		// For Google auth, check if user is in admin list (environment variable)
		if a.authMethod == "google" {
			adminUsers := getEnv("ADMIN_USERS", "")
			if adminUsers == "" {
				c.JSON(http.StatusForbidden, gin.H{"error": "No admin users configured"})
				c.Abort()
				return
			}

			userEmail := ownerID.(string)
			adminList := strings.Split(adminUsers, ",")
			isAdmin := false
			for _, admin := range adminList {
				if strings.TrimSpace(admin) == userEmail {
					isAdmin = true
					break
				}
			}

			if !isAdmin {
				c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: admin privileges required"})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// adminDashboard renders the admin dashboard page
func (a *AppController) adminDashboard(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	displayName := ownerID
	userPicture := ""
	
	if a.authMethod == "google" {
		name, okName := c.Get("user_name")
		pic, okPic := c.Get("user_picture")
		if okName {
			displayName = name.(string)
		}
		if okPic {
			userPicture = pic.(string)
		}
	} else if a.authMethod == "password" {
		displayName = "Admin (Password Auth)"
	}
	
	c.HTML(http.StatusOK, "admin-dashboard.html", gin.H{
		"title": "k8s Playground - Admin Dashboard", 
		"OwnerID": ownerID, 
		"DisplayName": displayName, 
		"UserPicture": userPicture, 
		"AuthMethod": a.authMethod,
	})
}

// getCommandLogs returns command logs for admin users
func (a *AppController) getCommandLogs(c *gin.Context) {
	// Parse query parameters
	userID := c.Query("user_id")
	environmentID := c.Query("environment_id")
	limitStr := c.DefaultQuery("limit", "100")
	offsetStr := c.DefaultQuery("offset", "0")

	limit := 100
	offset := 0
	
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}

	// Use internal API if available, otherwise fallback to direct access
	if a.loggingControllerAPIURL != "" && a.loggingAdminToken != "" {
		logs, err := a.fetchLogsFromAPI(userID, environmentID, limit, offset)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"logs": logs, "count": len(logs)})
			return
		}
		log.Printf("Failed to fetch logs from API, falling back to direct access: %v", err)
	}

	// Fallback to direct access
	logs, err := a.loggingController.GetCommandLogs(userID, environmentID, limit, offset)
	if err != nil {
		log.Printf("Error getting command logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve command logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs, "count": len(logs)})
}

// fetchLogsFromAPI calls the logging controller's internal API
func (a *AppController) fetchLogsFromAPI(userID, environmentID string, limit, offset int) ([]CommandLog, error) {
	url := fmt.Sprintf("%s/admin/logs?limit=%d&offset=%d", a.loggingControllerAPIURL, limit, offset)
	if userID != "" {
		url += "&user_id=" + userID
	}
	if environmentID != "" {
		url += "&environment_id=" + environmentID
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("X-Admin-Token", a.loggingAdminToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var response struct {
		Logs  []CommandLog `json:"logs"`
		Count int          `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return response.Logs, nil
}

// getAllEnvironments returns all environments for admin users
func (a *AppController) getAllEnvironments(c *gin.Context) {
	ctx := context.Background()
	environments, err := a.redisQueue.GetAllItems(ctx)
	if err != nil {
		log.Printf("Error getting all environments for admin: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get environments"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"environments": environments})
}

// getEnvironmentServices returns the list of services running in the DinD Pod
func (a *AppController) getEnvironmentServices(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	envID := c.Param("id")
	
	ctx := context.Background()
	item, err := a.redisQueue.GetItem(ctx, envID)
	if err != nil {
		if err.Error() == "item not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		} else {
			log.Printf("Error getting environment %s for services by owner %s: %v", envID, ownerID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve environment details"})
		}
		return
	}
	
	if item.Owner != ownerID {
		log.Printf("Forbidden: Owner %s attempted to access services for environment %s owned by %s", ownerID, envID, item.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the owner of this environment"})
		return
	}
	
	if item.Status != queue.StatusAvailable {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Environment is not available"})
		return
	}
	
	if item.PodID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pod ID not available"})
		return
	}
	
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	
	var podName string
	if item.WorkloadType == "deployment" {
		podName, err = a.k8sClient.GetPodNameForWorkload(c.Request.Context(), item.PodID, namespace)
		if err != nil {
			log.Printf("Failed to get pod name for workload %s (env %s): %v", item.PodID, envID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not find the running pod for the environment"})
			return
		}
	} else {
		podName = fmt.Sprintf("%s-0", item.PodID)
	}
	
	services, err := a.k8sClient.GetServicesInPod(c.Request.Context(), podName, namespace)
	if err != nil {
		log.Printf("Error getting services for pod %s in environment %s: %v", podName, envID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve services"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"services": services})
}

// proxyToPod proxies HTTP requests to services running inside the DinD Pod
func (a *AppController) proxyToPod(c *gin.Context) {
	ownerID := c.MustGet("owner_id").(string)
	envID := c.Param("id")
	path := c.Param("path")
	
	ctx := context.Background()
	item, err := a.redisQueue.GetItem(ctx, envID)
	if err != nil {
		if err.Error() == "item not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		} else {
			log.Printf("Error getting environment %s for proxy by owner %s: %v", envID, ownerID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve environment details"})
		}
		return
	}
	
	if item.Owner != ownerID {
		log.Printf("Forbidden: Owner %s attempted to proxy to environment %s owned by %s", ownerID, envID, item.Owner)
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the owner of this environment"})
		return
	}
	
	if item.Status != queue.StatusAvailable {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Environment is not available"})
		return
	}
	
	if item.PodID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pod ID not available"})
		return
	}
	
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}
	
	var podName string
	if item.WorkloadType == "deployment" {
		podName, err = a.k8sClient.GetPodNameForWorkload(c.Request.Context(), item.PodID, namespace)
		if err != nil {
			log.Printf("Failed to get pod name for workload %s (env %s): %v", item.PodID, envID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not find the running pod for the environment"})
			return
		}
	} else {
		podName = fmt.Sprintf("%s-0", item.PodID)
	}
	
	// Get the port from query parameters or use default
	port := c.DefaultQuery("port", "80")
	
	// For Kind cluster services, we need to proxy through the DinD container
	// since services are only accessible from within the cluster network
	a.proxyThroughDinDContainer(c, podName, namespace, port, path, c.Request)
}

// proxyThroughDinDContainer proxies HTTP requests by executing curl inside the DinD container
// This allows access to services running inside the Kind cluster
func (a *AppController) proxyThroughDinDContainer(c *gin.Context, podName, namespace, port, path string, req *http.Request) {
	// Create context with timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	
	// First, try to find the service and get the correct target
	// Look for services running on the specified port
	services, err := a.k8sClient.GetKindClusterServices(ctx, podName, namespace)
	if err != nil {
		log.Printf("Failed to get services for pod %s: %v", podName, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Failed to discover services",
			"details": fmt.Sprintf("Could not list services in pod %s", podName),
		})
		return
	}
	
	var targetService *k8s.ServiceInfo
	portInt, _ := strconv.Atoi(port)
	for _, svc := range services {
		if svc.Port == portInt {
			targetService = &svc
			break
		}
	}
	
	if targetService == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Service not found",
			"details": fmt.Sprintf("No service found on port %s", port),
		})
		return
	}
	
	// Build target URL using service name and port within the Kind cluster
	//targetURL := fmt.Sprintf("http://%s:%d%s", targetService.Name, targetService.Port, path)
	// targetService.Name は、DinD Pod 上で名前解決できない。127.0.0.1 固定でいい
	// targetService.Port は、port-forward ではPodのリッスンポートを直撃しないとダメ
	// 以下の実装では、サービスポートにアクセスすることになる
	targetURL := fmt.Sprintf("http://localhost:%d%s", targetService.Port, path)
	if req.URL.RawQuery != "" {
		// Remove the port parameter from query since we're using it for targeting
		params := req.URL.Query()
		params.Del("port")
		if encodedQuery := params.Encode(); encodedQuery != "" {
			targetURL += "?" + encodedQuery
		}
	}
	
	// Build headers for curl command
	var headerArgs []string
	for name, values := range req.Header {
		// Skip headers that shouldn't be forwarded
		if name == "Host" || name == "Content-Length" || strings.HasPrefix(name, "X-Forwarded-") {
			continue
		}
		for _, value := range values {
			headerArgs = append(headerArgs, "-H", fmt.Sprintf("'%s: %s'", name, value))
		}
	}
	
	// Build data arguments for POST/PUT requests
	var dataArg string
	var hasBody bool
	if req.Method == "POST" || req.Method == "PUT" || req.Method == "PATCH" {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil && len(bodyBytes) > 0 {
			hasBody = true
			// Escape the body for shell
			dataArg = fmt.Sprintf("--data-binary '%s'", strings.ReplaceAll(string(bodyBytes), "'", "'\"'\"'"))
		}
	}
	
	// Create a comprehensive bash command that:
	// 1. Starts kubectl port-forward in background
	// 2. Waits for port to be ready
	// 3. Executes curl request
	// 4. Cleans up port-forward
	var curlCmd string
	if hasBody {
		curlCmd = fmt.Sprintf("curl -s -i -X %s %s %s http://localhost:%s%s", 
			req.Method, strings.Join(headerArgs, " "), dataArg, port, path)
	} else {
		// Explicitly specify GET method to prevent HEAD requests
		method := req.Method
		if method == "" || method == "HEAD" {
			method = "GET"
		}
		curlCmd = fmt.Sprintf("curl -s -i -X %s %s http://localhost:%s%s", 
			method, strings.Join(headerArgs, " "), port, path)
	}
	
	bashScript := fmt.Sprintf(`
		# Start port-forward in background
		kubectl port-forward service/%s %s:%d > /dev/null 2>&1 &
		PF_PID=$!
		
		# Wait a fixed time for port-forward to be ready
		sleep 2
		
		# Execute curl request
		%s
		
		# Cleanup port-forward
		kill $PF_PID 2>/dev/null || true
		wait $PF_PID 2>/dev/null || true
	`, targetService.Name, port, targetService.Port, curlCmd)
	
	bashCmd := []string{"bash", "-c", bashScript}
	
	// Execute bash script inside the DinD container  
	var stdout, stderr strings.Builder
	
	// Debug: Log the bash script being executed
	log.Printf("Executing bash script in pod %s: %s", podName, bashScript)
	
	// Use the existing ExecInPod method but modify it for our needs
	err = a.executeHTTPProxy(ctx, podName, namespace, bashCmd, nil, &stdout, &stderr)

	if err != nil {
		stderrOutput := stderr.String()
		stdoutOutput := stdout.String()
		
		log.Printf("Failed to execute curl in pod %s: %v, stderr: %s, stdout: %s", podName, err, stderrOutput, stdoutOutput)
		
		// Check for specific error conditions
		if strings.Contains(err.Error(), "operation was canceled") || strings.Contains(err.Error(), "context deadline exceeded") {
			c.JSON(http.StatusRequestTimeout, gin.H{
				"error": "Request timeout",
				"details": "The request to the service timed out",
				"target": targetURL,
				"suggestion": "The service may be slow to respond or not running",
			})
		} else if strings.Contains(err.Error(), "exit code 56") || strings.Contains(stderrOutput, "Failed to connect") {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "Service connection failed",
				"details": "Could not connect to the service",
				"target": targetURL,
				"suggestion": "Verify the service is running and accessible on the specified port",
			})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "Failed to connect to service",
				"details": fmt.Sprintf("Could not reach service on port %s", port),
				"target": targetURL,
				"debug_stderr": stderrOutput,
				"debug_stdout": stdoutOutput,
			})
		}
		return
	}

	// Parse curl output (headers + body)
	output := stdout.String()
	stderrOutput := stderr.String()
	
	// Debug: Log the raw output
	log.Printf("Raw stdout from pod %s (length: %d): %q", podName, len(output), output)
	log.Printf("Raw stderr from pod %s (length: %d): %q", podName, len(stderrOutput), stderrOutput)
	
	if output == "" {
		log.Printf("Empty response from curl for %s", targetURL)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Empty response from service",
			"target": targetURL,
		})
		return
	}

	// Split headers and body
	parts := strings.SplitN(output, "\r\n\r\n", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(output, "\n\n", 2)
	}
	
	// Debug: Log the split results
	log.Printf("Split output into %d parts for %s", len(parts), targetURL)
	if len(parts) >= 1 {
		log.Printf("Headers section (length: %d): %q", len(parts[0]), parts[0])
	}
	if len(parts) >= 2 {
		log.Printf("Body section (length: %d): %q", len(parts[1]), parts[1][:min(200, len(parts[1]))])
	}
	
	if len(parts) < 2 {
		// No header separator found, treat entire output as body
		log.Printf("No header separator found for %s, treating as plain text", targetURL)
		c.Header("Content-Type", "text/plain")
		c.String(http.StatusOK, output)
		return
	}

	headerSection := parts[0]
	bodySection := parts[1]

	// Parse status line
	headerLines := strings.Split(headerSection, "\n")
	if len(headerLines) == 0 {
		log.Printf("No header lines found for %s", targetURL)
		c.String(http.StatusOK, bodySection)
		return
	}

	statusLine := strings.TrimSpace(headerLines[0])
	statusCode := http.StatusOK

	// Debug: Log status line parsing
	log.Printf("Status line for %s: %q", targetURL, statusLine)

	// Extract status code from HTTP status line
	if strings.HasPrefix(statusLine, "HTTP/") {
		statusParts := strings.Fields(statusLine)
		if len(statusParts) >= 2 {
			if code, err := strconv.Atoi(statusParts[1]); err == nil {
				statusCode = code
				log.Printf("Extracted status code %d for %s", statusCode, targetURL)
			} else {
				log.Printf("Failed to parse status code from %q for %s", statusParts[1], targetURL)
			}
		} else {
			log.Printf("Invalid status line format for %s: %q", targetURL, statusLine)
		}
	} else {
		log.Printf("Status line doesn't start with HTTP/ for %s: %q", targetURL, statusLine)
	}

	// Set response headers (skip status line)
	for i := 1; i < len(headerLines); i++ {
		line := strings.TrimSpace(headerLines[i])
		if line == "" {
			continue
		}
		
		colonIndex := strings.Index(line, ":")
		if colonIndex > 0 {
			name := strings.TrimSpace(line[:colonIndex])
			value := strings.TrimSpace(line[colonIndex+1:])
			
			// Skip headers that shouldn't be set
			if name == "Transfer-Encoding" || name == "Connection" || name == "Content-Length" {
				continue
			}
			
			c.Header(name, value)
		}
	}

	// Set CORS headers
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

	// Send response
	log.Printf("Sending response for %s: status=%d, body_length=%d", targetURL, statusCode, len(bodySection))
	c.String(statusCode, bodySection)
}

// executeHTTPProxy executes curl command inside the DinD container for HTTP proxying
func (a *AppController) executeHTTPProxy(ctx context.Context, podName, namespace string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if a.k8sClient == nil {
		return fmt.Errorf("k8s client is nil")
	}

	// Create the exec request directly since we need custom handling
	req := a.k8sClient.GetClientset().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "dind",
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(a.k8sClient.GetRestConfig(), "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
