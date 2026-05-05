#!/usr/bin/env python3
# mock_cloudpoint.py — 模拟 ROS2 点云与栅格地图发布节点
# 需要安装: pip install rclpy numpy
import rclpy
from rclpy.node import Node
from sensor_msgs.msg import PointCloud2, PointField
from nav_msgs.msg import OccupancyGrid, MapMetaData
import numpy as np


class MockSlam(Node):
    def __init__(self):
        super().__init__('mock_slam_node')
        self.cloud_pub = self.create_publisher(PointCloud2, '/rtabmap/cloud_map', 10)
        self.grid_pub = self.create_publisher(OccupancyGrid, '/rtabmap/grid_map', 10)
        self.timer = self.create_timer(0.1, self.timer_callback)  # 10Hz
        self.field_types = {
            'FLOAT32': PointField.FLOAT32,
            'INT8': PointField.INT8,
        }

    def _build_pointcloud(self, num_points: int = 5000) -> PointCloud2:
        """
        构建 7-float 步长的点云消息 (X, Y, Z, R, G, B, A)。
        颜色通道使用 [0, 255] 范围，前端通过 /255 解码为 [0, 1]。
        Alpha = 255 确保点在 WebGL 中完全不透明。
        """
        # 位置：散布在 100m × 100m 范围内，高度 0~30m
        x = np.random.uniform(-50.0, 50.0, num_points).astype(np.float32)
        y = np.random.uniform(-50.0, 50.0, num_points).astype(np.float32)
        z = np.random.uniform(0.0, 30.0, num_points).astype(np.float32)

        # 颜色：0-255 整型（前端 Cesium.Color 构造时除以 255）
        r = np.full(num_points, 255.0, dtype=np.float32)   # 纯红
        g = np.full(num_points, 0.0, dtype=np.float32)
        b = np.full(num_points, 0.0, dtype=np.float32)
        a = np.full(num_points, 255.0, dtype=np.float32)   # 完全不透明

        # 交错排列: [x0,y0,z0,r0,g0,b0,a0, x1,...]
        interleaved = np.column_stack([x, y, z, r, g, b, a]).ravel()

        msg = PointCloud2()
        msg.header.frame_id = 'map'
        msg.height = 1
        msg.width = num_points
        msg.point_step = 28   # 7 floats × 4 bytes
        msg.row_step = msg.point_step * num_points
        msg.is_dense = True

        msg.fields = [
            PointField(name='x', offset=0,  datatype=PointField.FLOAT32, count=1),
            PointField(name='y', offset=4,  datatype=PointField.FLOAT32, count=1),
            PointField(name='z', offset=8,  datatype=PointField.FLOAT32, count=1),
            PointField(name='r', offset=12, datatype=PointField.FLOAT32, count=1),
            PointField(name='g', offset=16, datatype=PointField.FLOAT32, count=1),
            PointField(name='b', offset=20, datatype=PointField.FLOAT32, count=1),
            PointField(name='a', offset=24, datatype=PointField.FLOAT32, count=1),
        ]
        msg.data = interleaved.tobytes()
        return msg

    def timer_callback(self):
        # 1. 3D 点云
        cloud_msg = self._build_pointcloud(5000)
        self.cloud_pub.publish(cloud_msg)

        # 2. 2D 栅格地图 (400×400 随机噪点)
        grid_msg = OccupancyGrid()
        grid_msg.info = MapMetaData(width=400, height=400, resolution=0.05)
        grid_msg.data = np.random.choice([-1, 0, 100], size=160000).astype(np.int8).tolist()
        self.grid_pub.publish(grid_msg)


if __name__ == '__main__':
    rclpy.init()
    node = MockSlam()
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        node.destroy_node()
        rclpy.shutdown()
