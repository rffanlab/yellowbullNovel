package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/rffanlab/yellowbullNovel/backend/internal/api"
	"github.com/rffanlab/yellowbullNovel/backend/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	port := flag.Int("port", 0, "HTTP 端口（覆盖配置文件）")
	flag.Parse()

	// 加载配置
	cfg := config.LoadOrDefault(*configPath)

	// 命令行参数覆盖
	if *port > 0 {
		cfg.Server.Port = *port
	}
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 8080
	}

	// 初始化日志
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("yellowbullNovel 后端启动",
		zap.String("configPath", *configPath),
		zap.Int("port", cfg.Server.Port),
		zap.String("projectRoot", cfg.Writing.ProjectRoot),
		zap.String("dbDriver", cfg.Database.Driver),
	)

	// 创建 API 服务器
	server, err := api.NewServer(cfg)
	if err != nil {
		logger.Fatal("创建服务器失败", zap.Error(err))
	}

	// 启动 HTTP 服务（非阻塞）
	go func() {
		if err := server.Run(cfg.Server.Port); err != nil {
			logger.Fatal("HTTP 服务启动失败", zap.Error(err))
		}
	}()

	logger.Info("HTTP API 服务器已启动", zap.Int("port", cfg.Server.Port))

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("收到关闭信号，正在优雅关闭...")

	// 优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("优雅关闭失败", zap.Error(err))
	}

	logger.Info("服务器已关闭")
}
