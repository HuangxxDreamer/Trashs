#!/usr/bin/env python3
"""
save_pcd.py — 订阅 ROS 2 PointCloud2 话题，捕获一帧并保存为 ASCII PCD 文件。
用法: python3 save_pcd.py <topic> <output_path>
示例: python3 save_pcd.py /rtabmap/cloud_map /path/to/saved_maps/cloud_20260505_170000.pcd
依赖: rclpy, sensor_msgs, numpy, std_msgs (均为 ROS 2 基础包，无需额外安装)
"""
import sys
import struct
import time
import numpy as np
import rclpy
from rclpy.node import Node
from sensor_msgs.msg import PointCloud2, PointField


class PCDSaver(Node):
    def __init__(self, topic: str, output_path: str):
        super().__init__('pcd_saver')
        self.output_path = output_path
        self.received = False
        self.sub = self.create_subscription(
            PointCloud2, topic, self.callback, 10
        )
        self.get_logger().info(f'等待话题 {topic} 上的点云消息...')

    def callback(self, msg: PointCloud2):
        if self.received:
            return
        self.received = True

        self.get_logger().info(f'收到点云: {msg.width} 点, 正在保存到 {self.output_path}')
        self._write_pcd(msg, self.output_path)
        self.get_logger().info('PCD 保存完成，退出。')

    def _build_field_map(self, fields: list[PointField]) -> dict[str, dict]:
        """解析 PointCloud2 的 fields 列表，建立字段名→{offset, dtype} 映射。"""
        dtype_map = {
            PointField.INT8:    ('b', 1),
            PointField.UINT8:   ('B', 1),
            PointField.INT16:   ('h', 2),
            PointField.UINT16:  ('H', 2),
            PointField.INT32:   ('i', 4),
            PointField.UINT32:  ('I', 4),
            PointField.FLOAT32: ('f', 4),
            PointField.FLOAT64: ('d', 8),
        }
        field_map = {}
        for f in fields:
            info = dtype_map.get(f.datatype)
            if info:
                field_map[f.name] = {'offset': f.offset, 'fmt': info[0], 'size': info[1]}
        return field_map

    def _read_field(self, buf: bytes, offset: int, fmt: str) -> float:
        """从字节缓冲区读取一个数值字段，返回 float。"""
        value = struct.unpack_from(fmt, buf, offset)[0]
        return float(value)

    def _write_pcd(self, msg: PointCloud2, path: str):
        """将 PointCloud2 消息写为 ASCII PCD 文件 (VERSION 0.7)。"""
        field_map = self._build_field_map(msg.fields)
        point_step = msg.point_step

        # 确定输出字段：优先 x/y/z 作为坐标，若缺失则用前三个字段
        coord_fields = []
        for key in ('x', 'y', 'z'):
            if key in field_map:
                coord_fields.append(key)

        if len(coord_fields) < 3:
            # 回退：使用 fields 列表中的前三个
            coord_fields = [f.name for f in msg.fields[:3]]

        # 尝试提取 rgb / rgba 字段
        color_field = None
        for key in ('rgb', 'rgba', 'intensity', 'r'):
            if key in field_map:
                color_field = key
                break

        x_field = coord_fields[0]
        y_field = coord_fields[1]
        z_field = coord_fields[2]

        # 构建 PCD 头部
        fields_str = 'x y z'
        if color_field:
            fields_str += ' rgb'

        header = f"""# .PCD v0.7 - Point Cloud Data file format
VERSION 0.7
FIELDS {fields_str}
SIZE 4 4 4{" 4" if color_field else ""}
TYPE F F F{" U" if color_field else ""}
COUNT 1 1 1{" 1" if color_field else ""}
WIDTH {msg.width}
HEIGHT {msg.height}
VIEWPOINT 0 0 0 1 0 0 0
POINTS {msg.width * msg.height}
DATA ascii
"""

        with open(path, 'w') as f:
            f.write(header)

            raw = msg.data
            for i in range(msg.width * msg.height):
                base = i * point_step
                x = self._read_field(raw, base + field_map[x_field]['offset'], field_map[x_field]['fmt'])
                y = self._read_field(raw, base + field_map[y_field]['offset'], field_map[y_field]['fmt'])
                z = self._read_field(raw, base + field_map[z_field]['offset'], field_map[z_field]['fmt'])

                if color_field:
                    rgb_val = self._read_field(raw, base + field_map[color_field]['offset'], field_map[color_field]['fmt'])
                    # RGB 打包为 float (PCD 使用 packed RGB)
                    if color_field == 'rgb':
                        # ROS PointCloud2 的 rgb 字段是 uint32 packed RGB
                        packed = int(rgb_val)
                        r = (packed >> 16) & 0xFF
                        g = (packed >> 8) & 0xFF
                        b = packed & 0xFF
                        # PCD RGB 浮点 = 4 字节 [R, G, B, 0]
                        rgb_float = struct.unpack('f', struct.pack('BBBB', r, g, b, 0))[0]
                        f.write(f'{x:.6f} {y:.6f} {z:.6f} {rgb_float:.6f}\n')
                    elif color_field in ('r', 'intensity'):
                        # 单通道灰度，R=G=B=intensity
                        v = min(255, max(0, int(rgb_val)))
                        rgb_float = struct.unpack('f', struct.pack('BBBB', v, v, v, 0))[0]
                        f.write(f'{x:.6f} {y:.6f} {z:.6f} {rgb_float:.6f}\n')
                    else:
                        # rgba: 取前 3 通道
                        f.write(f'{x:.6f} {y:.6f} {z:.6f} {rgb_val:.6f}\n')
                else:
                    f.write(f'{x:.6f} {y:.6f} {z:.6f}\n')

        self.get_logger().info(f'共写入 {msg.width * msg.height} 个点')


def main():
    rclpy.init()
    if len(sys.argv) < 3:
        print(f'用法: {sys.argv[0]} <topic> <output_path>')
        sys.exit(1)

    topic = sys.argv[1]
    output_path = sys.argv[2]
    node = PCDSaver(topic, output_path)

    try:
        while rclpy.ok() and not node.received:
            rclpy.spin_once(node, timeout_sec=0.1)
    except KeyboardInterrupt:
        pass
    finally:
        node.destroy_node()
        rclpy.shutdown()


if __name__ == '__main__':
    main()
