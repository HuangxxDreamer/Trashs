#!/usr/bin/env python3
"""
pcd_to_3dtiles.py — 将 ASCII PCD 文件转换为 3D Tiles 1.0 (pnts + tileset.json)。
零外部依赖（仅 Python 标准库），替代 py3dtiles。

用法: python3 pcd_to_3dtiles.py <input.pcd> <output_dir>
示例: python3 pcd_to_3dtiles.py cloud.pcd ./3dtiles_output
"""
import sys
import os
import struct
import json
import math


def parse_ascii_pcd(path: str) -> tuple[list[tuple[float, float, float]], list[tuple[int, int, int]] | None]:
    """解析 ASCII 格式 PCD 文件，返回 (positions, colors) 列表。"""
    with open(path, 'r') as f:
        lines = f.readlines()

    header_end = 0
    fields = []
    points_count = 0
    has_rgb = False

    for i, line in enumerate(lines):
        line = line.strip()
        if line.startswith('FIELDS'):
            fields = line.split()[1:]
            if 'rgb' in fields:
                has_rgb = True
        elif line.startswith('POINTS'):
            points_count = int(line.split()[1])
        elif line.startswith('DATA'):
            header_end = i + 1
            break

    if points_count == 0:
        raise ValueError("PCD 文件中没有点数据")

    positions = []
    colors = []

    for line in lines[header_end:]:
        line = line.strip()
        if not line:
            continue
        parts = line.split()
        if len(parts) < 3:
            continue

        x, y, z = float(parts[0]), float(parts[1]), float(parts[2])
        positions.append((x, y, z))

        if has_rgb and len(parts) >= 4:
            # PCD rgb 字段是 packed float → 解出 R,G,B
            rgb_float = float(parts[3])
            rgb_bytes = struct.pack('f', rgb_float)
            r, g, b = rgb_bytes[0], rgb_bytes[1], rgb_bytes[2]
            colors.append((r, g, b))
        else:
            colors.append((255, 255, 255))

    return positions, colors


def write_pnts(output_path: str, positions: list, colors: list):
    """将点云写入 .pnts 文件 (3D Tiles Point Cloud 格式)。"""
    point_count = len(positions)

    # Feature Table JSON
    feature_json = json.dumps({
        "POINTS_LENGTH": point_count,
        "POSITION": {"byteOffset": 0}
    }, separators=(',', ':'))

    # Batch Table JSON (RGB 颜色)
    batch_json = json.dumps({
        "RGB": {"byteOffset": 0, "componentType": "UNSIGNED_BYTE", "type": "VEC3"}
    }, separators=(',', ':'))

    # 计算各段字节长度与对齐填充
    HEADER_SIZE = 28

    def pad8(size: int) -> int:
        return (8 - (size % 8)) % 8

    ft_json_len = len(feature_json.encode('utf-8'))
    ft_json_pad = pad8(HEADER_SIZE + ft_json_len)

    # 每个点 3 个 float32 (x, y, z)
    ft_binary_len = point_count * 3 * 4

    bt_json_start = HEADER_SIZE + ft_json_len + ft_json_pad + ft_binary_len
    bt_json_len = len(batch_json.encode('utf-8'))
    bt_json_pad = pad8(bt_json_start + bt_json_len)

    bt_binary_len = point_count * 3  # R, G, B 各 1 字节

    total_len = (HEADER_SIZE + ft_json_len + ft_json_pad +
                 ft_binary_len + bt_json_len + bt_json_pad + bt_binary_len)

    # 写入文件
    with open(output_path, 'wb') as f:
        # Header (28 bytes)
        f.write(b'pnts')                           # magic
        f.write(struct.pack('<I', 1))               # version
        f.write(struct.pack('<I', total_len))        # byteLength
        f.write(struct.pack('<I', ft_json_len))      # featureTableJSONByteLength
        f.write(struct.pack('<I', ft_binary_len))    # featureTableBinaryByteLength
        f.write(struct.pack('<I', bt_json_len))      # batchTableJSONByteLength
        f.write(struct.pack('<I', bt_binary_len))    # batchTableBinaryByteLength

        # Feature Table JSON
        f.write(feature_json.encode('utf-8'))
        f.write(b' ' * ft_json_pad)

        # Feature Table Binary: [x1, y1, z1, x2, y2, z2, ...]
        for x, y, z in positions:
            f.write(struct.pack('<fff', x, y, z))

        # Batch Table JSON
        f.write(batch_json.encode('utf-8'))
        f.write(b' ' * bt_json_pad)

        # Batch Table Binary: [r1, g1, b1, r2, g2, b2, ...]
        for r, g, b in colors:
            f.write(struct.pack('BBB', r, g, b))

    return total_len


def write_tileset(output_dir: str, pnts_filename: str, positions: list):
    """写入 tileset.json 描述文件。"""
    # 计算包围盒
    xs = [p[0] for p in positions]
    ys = [p[1] for p in positions]
    zs = [p[2] for p in positions]

    min_x, max_x = min(xs), max(xs)
    min_y, max_y = min(ys), max(ys)
    min_z, max_z = min(zs), max(zs)

    # 几何误差取包围盒对角线的 1/20
    diagonal = math.sqrt((max_x - min_x)**2 + (max_y - min_y)**2 + (max_z - min_z)**2)
    geometric_error = max(diagonal / 20.0, 1.0)

    tileset = {
        "asset": {
            "version": "1.0"
        },
        "geometricError": geometric_error * 10,
        "root": {
            "boundingVolume": {
                "box": [
                    (min_x + max_x) / 2.0,
                    (min_y + max_y) / 2.0,
                    (min_z + max_z) / 2.0,
                    (max_x - min_x) / 2.0, 0.0, 0.0,
                    0.0, (max_y - min_y) / 2.0, 0.0,
                    0.0, 0.0, (max_z - min_z) / 2.0
                ]
            },
            "geometricError": geometric_error,
            "refine": "ADD",
            "content": {
                "uri": pnts_filename
            }
        }
    }

    with open(os.path.join(output_dir, 'tileset.json'), 'w') as f:
        json.dump(tileset, f, indent=2)


def main():
    if len(sys.argv) < 3:
        print(f'用法: {sys.argv[0]} <input.pcd> <output_dir>')
        print(f'示例: {sys.argv[0]} cloud.pcd ./3dtiles_output')
        sys.exit(1)

    pcd_path = sys.argv[1]
    output_dir = sys.argv[2]

    if not os.path.exists(pcd_path):
        print(f'错误: 输入文件不存在: {pcd_path}')
        sys.exit(1)

    os.makedirs(output_dir, exist_ok=True)

    print(f'读取 PCD: {pcd_path}')
    positions, colors = parse_ascii_pcd(pcd_path)
    print(f'  解析完成: {len(positions)} 个点')

    pnts_filename = 'points.pnts'
    pnts_path = os.path.join(output_dir, pnts_filename)

    print(f'写入 .pnts: {pnts_path}')
    total_bytes = write_pnts(pnts_path, positions, colors)
    print(f'  写入完成: {total_bytes:,} 字节')

    print(f'写入 tileset.json')
    write_tileset(output_dir, pnts_filename, positions)

    print(f'3D Tiles 转换完成 → {output_dir}/')


if __name__ == '__main__':
    main()
