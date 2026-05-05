#!/bin/bash
# 激活 ROS 2
source /opt/ros/humble/setup.bash

# 配置 Cgo 变量，增加 [ -d "$d" ] 判断，只提取目录，过滤掉文件
export CGO_CFLAGS="-I/opt/ros/humble/include $(for d in /opt/ros/humble/include/*; do if [ -d "$d" ]; then echo -n "-I$d "; fi; done)"

export CGO_LDFLAGS="-L/opt/ros/humble/lib -Wl,-rpath=/opt/ros/humble/lib"

# 编译项目
go build -o dog-stream-gateway ./cmd/dog-stream-gateway/main.go
echo "编译完成！"