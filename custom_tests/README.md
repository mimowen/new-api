# 自定义测试脚本说明

## 概述
此文件夹包含测试模型拦截器的脚本，不会与官方测试冲突。

## 文件说明

- `test_model_rank.py` - 主要测试脚本

## 使用方法

### 安装依赖
```bash
pip install requests
```

### 运行测试
```bash
cd custom_tests
python test_model_rank.py
```

## 测试内容

1. **模型失败自动切换** - 测试当一个模型失败时，是否会尝试下一个
2. **排名优先级验证** - 观察模型分数变化后，优先级是否相应调整

## 注意事项

- 使用 `default` 分组测试，避免消耗付费模型
- 控制测试频率，避免产生过多费用
- 可以先查看 `model_mapping.yaml` 中的配置
