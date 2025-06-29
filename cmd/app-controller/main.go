// cmd/app-controller/main.go
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json" // JSONパース用に追加
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
	"github.com/tyottodekiru/k8s-playground/internal/controllers"
	"github.com/tyottodekiru/k8s-playground/pkg/queue"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleoauth2 "google.golang.org/api/oauth2/v2"
)

func main() {
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	port := getEnv("PORT", "8080")
	sessionKey := getEnv("SESSION_KEY", "")
	baseURL := getEnv("BASE_URL", "http://localhost:8080")
	authMethod := getEnv("AUTH_METHOD", "google")
	ginMode := getEnv("GIN_MODE", "debug")
	googleAllowedDomainsRaw := getEnv("GOOGLE_ALLOWED_DOMAINS", "")
	dindImageVersionsJSON := getEnv("DIND_IMAGE_VERSIONS_JSON", "{}") // ★ DinDバージョン情報を読み込む
	// ★ デフォルトのワークロードタイプを環境変数から読み込む
	dindWorkloadType := getEnv("DIND_WORKLOAD_TYPE", "statefulset")
	loggingControllerAPIURL := getEnv("LOGGING_CONTROLLER_API_URL", "")
	loggingAdminToken := getEnv("LOGGING_ADMIN_TOKEN", "")

	if sessionKey == "" {
		log.Println("Warning: SESSION_KEY is not set. Generating a random key for temporary use. Set a persistent key in production.")
		key := make([]byte, 64)
		_, err := rand.Read(key)
		if err != nil {
			log.Fatalf("Failed to generate random session key: %v", err)
		}
		sessionKey = base64.StdEncoding.EncodeToString(key)
	}

	var oauth2Config *oauth2.Config
	var legacyAuthPassword string
	var googleAllowedDomainsList []string
	var dindImageVersionsMap map[string]string // ★ DinDバージョン情報を格納するマップ

	// DinDバージョン情報のJSONをパース
	log.Printf("DIND_IMAGE_VERSIONS_JSON: %s", dindImageVersionsJSON)
	if err := json.Unmarshal([]byte(dindImageVersionsJSON), &dindImageVersionsMap); err != nil {
		log.Printf("Warning: Failed to parse DIND_IMAGE_VERSIONS_JSON: %v. Using fallback versions. JSON was: %s", err, dindImageVersionsJSON)
		// パース失敗時は、Helm values.yamlのデフォルト値を使用
		dindImageVersionsMap = map[string]string{
			"1.33": "k8s-1.33.0",
			"1.32": "k8s-1.32.1",
			"1.31": "k8s-1.31.2", 
			"1.30": "k8s-1.30.2",
		}
		log.Printf("Using fallback DinD versions: %+v", dindImageVersionsMap)
	}
	
	// 空のマップの場合もデフォルト値を使用
	if len(dindImageVersionsMap) == 0 {
		log.Printf("DinD versions map is empty. Using fallback versions.")
		dindImageVersionsMap = map[string]string{
			"1.33": "k8s-1.33.0",
			"1.32": "k8s-1.32.1", 
			"1.31": "k8s-1.31.2",
			"1.30": "k8s-1.30.2",
		}
	}
	
	log.Printf("Final DinD versions: %+v", dindImageVersionsMap)


	if authMethod == "google" {
		googleClientID := getEnv("GOOGLE_CLIENT_ID", "")
		googleClientSecret := getEnv("GOOGLE_CLIENT_SECRET", "")
		if googleClientID == "" || googleClientSecret == "" {
			log.Fatalf("AUTH_METHOD is 'google', but GOOGLE_CLIENT_ID or GOOGLE_CLIENT_SECRET is not set.")
		}
		oauth2Config = &oauth2.Config{
			ClientID:     googleClientID,
			ClientSecret: googleClientSecret,
			RedirectURL:  baseURL + "/auth/google/callback",
			Scopes: []string{
				googleoauth2.OpenIDScope,
				googleoauth2.UserinfoEmailScope,
				googleoauth2.UserinfoProfileScope,
			},
			Endpoint: google.Endpoint,
		}
		log.Println("Authentication mode: Google OAuth2")
		if googleAllowedDomainsRaw != "" {
			googleAllowedDomainsList = strings.Split(googleAllowedDomainsRaw, ",")
			for i, domain := range googleAllowedDomainsList {
				googleAllowedDomainsList[i] = strings.TrimSpace(domain)
			}
			log.Printf("Restricting login to Google Workspace domains: %v", googleAllowedDomainsList)
		} else {
			log.Println("No domain restriction for Google login (any Google account allowed).")
		}
	} else if authMethod == "password" {
		legacyAuthPassword = getEnv("AUTH_PASSWORD", "admin123")
		log.Printf("Authentication mode: Legacy Password (Password: %s)", legacyAuthPassword)
	} else {
		log.Fatalf("Invalid AUTH_METHOD: %s. Must be 'google' or 'password'.", authMethod)
	}

	redisQueue, err := queue.NewRedisQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize Redis queue: %v", err)
	}
	defer redisQueue.Close()

	store := sessions.NewCookieStore([]byte(sessionKey))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   ginMode == "release",
		SameSite: http.SameSiteLaxMode,
	}

	appController := controllers.NewAppController(
		redisQueue,
		oauth2Config,
		store,
		authMethod,
		legacyAuthPassword,
		googleAllowedDomainsList,
		dindImageVersionsMap, // ★ AppControllerにDinDバージョンマップを渡す
		dindWorkloadType,     // ★ AppControllerにデフォルトのワークロードタイプを渡す
		loggingControllerAPIURL,
		loggingAdminToken,
	)

	if ginMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.Default()
	appController.SetupRoutes(router)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	go func() {
		log.Printf("Starting app controller on port %s with %s authentication in %s mode", port, authMethod, ginMode)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
