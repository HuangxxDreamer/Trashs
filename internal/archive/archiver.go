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

		// 1. 保存 2D 地图为 PNG
		mapPath := filepath.Join(saveDir, "map_latest.png")
		if err := os.WriteFile(mapPath, latestMapRaw, 0644); err != nil {
			log.Error().Err(err).Str("Path", mapPath).Msg("[Archiver] 保存 2D 地图失败")
		} else {
			log.Info().Str("Path", mapPath).Msg("[Archiver] 2D 地图已保存")
		}

		// 2. 执行 ROS 命令导出 PCD
		pcdFile := "cloud_" + timestamp + ".pcd"
		pcdPath := filepath.Join(saveDir, pcdFile)

		// 构造 ROS 命令 (示例：rosrun pcl_ros pointcloud_to_pcd input:=/topic)
		// 注意：这里的命令可能需要根据实际环境调整，比如 cd 到 saveDir 执行
		exportCmdStr := config.Cfg.Archive.RosExportCmd
		if exportCmdStr != "" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Cfg.Archive.Timeout)*time.Second)
			defer cancel()

			log.Info().Str("Cmd", exportCmdStr).Msg("[Archiver] 正在导出 PCD 文件...")

			// 修复环境变量缺失问题：通过 bash -c 执行，并预先 source ROS 环境
			// 这里的 /opt/ros/humble/setup.bash 建议根据实际环境调整
			fullCmd := fmt.Sprintf("source /opt/ros/humble/setup.bash && %s", exportCmdStr)
			cmd := exec.CommandContext(ctx, "bash", "-c", fullCmd)
			cmd.Dir = saveDir // 在保存目录执行，方便生成文件

			if output, err := cmd.CombinedOutput(); err != nil {
				log.Error().Err(err).Str("Output", string(output)).Msg("[Archiver] ROS 导出 PCD 失败")
			} else {
				log.Info().Msg("[Archiver] ROS 导出 PCD 成功")
			}
		}

		// 3. 执行 py3dtiles convert
		// 假设目录下已经有了导出的 PCD 或者其他格式
		// py3dtiles convert <input> --output <output_dir>
		tilesDir := filepath.Join(saveDir, "3dtiles")
		if _, err := os.Stat(tilesDir); os.IsNotExist(err) {
			os.MkdirAll(tilesDir, 0755)
		}

		log.Info().Msg("[Archiver] 正在启动 py3dtiles 转换 (3D Tiles)...")
		ctx3d, cancel3d := context.WithTimeout(context.Background(), time.Duration(config.Cfg.Archive.Timeout)*time.Second)
		defer cancel3d()

		// 这里假设我们将刚才生成的 pcd 转换为 3dtiles
		// 实际命令根据 py3dtiles 安装情况调整
		convertCmd := exec.CommandContext(ctx3d, "py3dtiles", "convert", pcdPath, "--output", tilesDir, "--overwrite")
		if output, err := convertCmd.CombinedOutput(); err != nil {
			log.Error().Err(err).Str("Output", string(output)).Msg("[Archiver] py3dtiles 转换失败")
		} else {
			log.Info().Str("OutputDir", tilesDir).Msg("[Archiver] 3D Tiles 转换完成")
		}

		log.Info().Msg("[Archiver] 所有归档任务执行完毕")
	}()
}
