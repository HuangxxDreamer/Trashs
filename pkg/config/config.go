package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

// Config 定义了整个网关的全局配置项
type Config struct {
	Server struct {
		Port     int    // 服务监听端口，默认 8080
		LogLevel string // 日志等级：debug, info, warn, error
	}

	ROS struct {
		DomainID   int    // ROS2 的 Domain ID
		Namespace  string // 节点命名空间
		NodeName   string // 本网关在 ROS 网络中的节点名称
		TopicCloud string // 订阅的 3D 点云话题名称
		TopicGrid  string // 订阅的 2D 栅格地图话题名称
	}

	WebRTC struct {
		PortMin           uint16 // WebRTC UDP 最小端口
		PortMax           uint16 // WebRTC UDP 最大端口
		BufferThresholdKB int    // DataChannel 的缓冲阈值（KB），超过则触发主动丢帧
	}

	Processing struct {
		VoxelSize              float64 // 降采样的体素网格大小（米）
		MaxPoints              int     // 单帧点云允许的最大点数，超出的将被截断或丢弃
		GridCompressionQuality int     // 2D 栅格地图压缩为 PNG/WebP 时的质量系数 (1-100)
	}
}

// Cfg 是一个全局共享的配置指针，整个系统在启动后仅可读
var Cfg *Config

// LoadConfig 从 .env 文件或环境变量加载配置信息
func LoadConfig() {
	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	// 环境变量的下划线替换，方便使用类似 ROS_MASTER_URI 的环境变量
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 设置默认值
	viper.SetDefault("PORT", 8080)
	viper.SetDefault("LOG_LEVEL", "info")

	viper.SetDefault("ROS_DOMAIN_ID", 0)
	viper.SetDefault("ROS_NAMESPACE", "")
	viper.SetDefault("ROS_NODE_NAME", "dog_stream_gateway")
	viper.SetDefault("TOPIC_CLOUD", "/rtabmap/cloud_map")
	viper.SetDefault("TOPIC_GRID", "/rtabmap/grid_map")

	viper.SetDefault("WEBRTC_PORT_MIN", 40000)
	viper.SetDefault("WEBRTC_PORT_MAX", 50000)
	viper.SetDefault("BUFFER_THRESHOLD_KB", 500)

	viper.SetDefault("VOXEL_SIZE", 0.1)
	viper.SetDefault("MAX_POINTS", 8000)
	viper.SetDefault("GRID_COMPRESSION_QUALITY", 80)

	_ = viper.ReadInConfig()

	Cfg = &Config{}

	Cfg.Server.Port = viper.GetInt("PORT")
	Cfg.Server.LogLevel = viper.GetString("LOG_LEVEL")

	Cfg.ROS.DomainID = viper.GetInt("ROS_DOMAIN_ID")
	Cfg.ROS.Namespace = viper.GetString("ROS_NAMESPACE")
	Cfg.ROS.NodeName = viper.GetString("ROS_NODE_NAME")
	Cfg.ROS.TopicCloud = viper.GetString("TOPIC_CLOUD")
	Cfg.ROS.TopicGrid = viper.GetString("TOPIC_GRID")

	Cfg.WebRTC.PortMin = uint16(viper.GetUint("WEBRTC_PORT_MIN"))
	Cfg.WebRTC.PortMax = uint16(viper.GetUint("WEBRTC_PORT_MAX"))
	Cfg.WebRTC.BufferThresholdKB = viper.GetInt("BUFFER_THRESHOLD_KB")

	Cfg.Processing.VoxelSize = viper.GetFloat64("VOXEL_SIZE")
	Cfg.Processing.MaxPoints = viper.GetInt("MAX_POINTS")
	Cfg.Processing.GridCompressionQuality = viper.GetInt("GRID_COMPRESSION_QUALITY")

	log.Printf("[Config] 全局配置加载成功: %+v", Cfg)
}
