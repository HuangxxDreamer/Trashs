package archive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"dog-stream-gateway/pkg/config"

	"github.com/rs/zerolog/log"
)

// Archiver 负责异步处理地图归档任务，包括 PNG 保存、PCD 导出和 3D Tiles 转换
type Archiver struct {
	wg          *sync.WaitGroup
	isArchiving atomic.Bool
}

// NewArchiver 创建一个归档管理器实例
func NewArchiver(wg *sync.WaitGroup) *Archiver {
	// 确保保存目录存在
	saveDir := config.Cfg.Archive.SaveDir
	if _, err := os.Stat(saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(saveDir, 0755); err != nil {
			log.Error().Err(err).Str("Dir", saveDir).Msg("创建归档目录失败")
		}
	}
	return &Archiver{
		wg: wg,
	}
}

// StartArchive 异步启动归档流程，立即返回
func (a *Archiver) StartArchive(latestMapRaw []byte) {
	// 并发锁与防抖：确保同一时刻只有一个归档任务在运行
	if !a.isArchiving.CompareAndSwap(false, true) {
		log.Warn().Msg("[Archiver] 已经有一个归档任务正在运行，忽略重复指令。")
		return
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.isArchiving.Store(false) // 任务结束后释放锁

		log.Info().Msg("[Archiver] 收到结束建图指令，开始执行异步归档流程...")

		saveDir := config.Cfg.Archive.SaveDir
		timestamp := time.Now().Format("20060102_150405")

		// 1. 保存 2D 地图为 PNG（带时间戳）
		mapFile := fmt.Sprintf("map_%s.png", timestamp)
		mapPath := filepath.Join(saveDir, mapFile)
		if err := os.WriteFile(mapPath, latestMapRaw, 0644); err != nil {
			log.Error().Err(err).Str("Path", mapPath).Msg("[Archiver] 保存 2D 地图失败")
		} else {
			log.Info().Str("Path", mapPath).Msg("[Archiver] 2D 地图已保存")
		}

		// 2. 执行 ROS 命令导出 PCD
		pcdPrefix := "cloud_" + timestamp
		pcdPath := filepath.Join(saveDir, pcdPrefix+".pcd")

		exportCmdStr := config.Cfg.Archive.RosExportCmd
		if exportCmdStr != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// 将完整输出路径作为参数追加到命令末尾
			fullCmd := fmt.Sprintf("source /opt/ros/humble/setup.bash && %s %s", exportCmdStr, pcdPath)
			log.Info().Str("Cmd", fullCmd).Msg("[Archiver] 正在导出 PCD 文件...")

			cmd := exec.CommandContext(ctx, "bash", "-c", fullCmd)

			if output, err := cmd.CombinedOutput(); err != nil {
				log.Error().Err(err).Str("Output", string(output)).Msg("[Archiver] ROS 导出 PCD 失败")
			} else {
				log.Info().Str("Output", string(output)).Msg("[Archiver] ROS 导出 PCD 完成")
			}

			// 验证 PCD 文件是否确实生成
			if _, err := os.Stat(pcdPath); os.IsNotExist(err) {
				log.Warn().Str("Path", pcdPath).Msg("[Archiver] PCD 文件未生成（话题无数据或超时），跳过 3D Tiles 转换")
				pcdPath = ""
			}
		}

		// 3. 3D Tiles 转换（仅当 PCD 文件已被成功导出）
		if pcdPath != "" {
			tilesDir := filepath.Join(saveDir, fmt.Sprintf("3dtiles_%s", timestamp))
			if _, err := os.Stat(tilesDir); os.IsNotExist(err) {
				os.MkdirAll(tilesDir, 0755)
			}

			log.Info().Str("Input", pcdPath).Str("OutputDir", tilesDir).Msg("[Archiver] 正在启动 3D Tiles 转换...")
			ctx3d, cancel3d := context.WithTimeout(context.Background(), time.Duration(config.Cfg.Archive.Timeout)*time.Second)
			defer cancel3d()

			fullCmd := fmt.Sprintf("python3 scripts/pcd_to_3dtiles.py %s %s", pcdPath, tilesDir)
			convertCmd := exec.CommandContext(ctx3d, "bash", "-c", fullCmd)
			if output, err := convertCmd.CombinedOutput(); err != nil {
				log.Error().Err(err).Str("Output", string(output)).Msg("[Archiver] 3D Tiles 转换失败")
			} else {
				log.Info().Str("Output", string(output)).Str("OutputDir", tilesDir).Msg("[Archiver] 3D Tiles 转换完成")
			}
		} else {
			log.Warn().Msg("[Archiver] PCD 文件缺失，跳过 3D Tiles 转换")
		}

		log.Info().Msg("[Archiver] 所有归档任务执行完毕")
	}()
}
