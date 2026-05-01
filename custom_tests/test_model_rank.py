#!/usr/bin/env python3
import requests
import time
import json
import sys
from typing import Dict, Any

BASE_URL = "http://localhost:3000"
API_KEY = "sk-XENzRnIqeYEApfilOmJCYN5rxQ4bnQN6pQ0VHmYtR7wgi6QN"


def get_model_rank_status() -> Dict[str, Any]:
    url = f"{BASE_URL}/api/model_rank/status"
    headers = {
        "Authorization": f"Bearer {API_KEY}"
    }
    response = requests.get(url, headers=headers)
    response.raise_for_status()
    return response.json()


def make_test_request(model_name: str = "gpt-4") -> tuple[bool, str, Dict[str, Any]]:
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {API_KEY}"
    }
    data = {
        "model": model_name,
        "messages": [{"role": "user", "content": "你好，请回复'测试成功'"}],
        "temperature": 0.7,
        "max_tokens": 50
    }
    try:
        response = requests.post(url, json=data, headers=headers, timeout=30)
        success = response.status_code == 200
        status = f"HTTP {response.status_code}"
        try:
            response_data = response.json()
        except:
            response_data = {"raw": response.text}
        return success, status, response_data
    except Exception as e:
        return False, f"Error: {str(e)}", {}


def print_separator(title: str) -> None:
    print("\n" + "=" * 60)
    print(title)
    print("=" * 60)


def test_1_failover() -> None:
    print_separator("测试 1: 模型失败自动切换")

    print("初始模型排名状态:")
    init_status = get_model_rank_status()
    for category, data in init_status.get("data", {}).items():
        print(f"\n  {category}:")
        for model in data.get("models", []):
            print(f"    - {model['model']} (score: {model['score']})")

    print("\n发送第一个请求...")
    success, status, response = make_test_request()
    print(f"  状态: {status}, 成功: {success}")
    if success:
        try:
            print(f"  响应: {json.dumps(response, ensure_ascii=False)[:500]}")
        except:
            pass

    print("\n发送第二个请求...")
    time.sleep(3)
    success2, status2, response2 = make_test_request()
    print(f"  状态: {status2}, 成功: {success2}")
    if success2:
        try:
            print(f"  响应: {json.dumps(response2, ensure_ascii=False)[:500]}")
        except:
            pass

    print("\n当前模型排名状态:")
    final_status = get_model_rank_status()
    for category, data in final_status.get("data", {}).items():
        print(f"\n  {category}:")
        for model in data.get("models", []):
            print(f"    - {model['model']} (score: {model['score']})")


def test_2_rank_priority() -> None:
    print_separator("测试 2: 模型排名优先级变化")

    print("初始模型排名 (default group):")
    init_status = get_model_rank_status()
    default_models = init_status.get("data", {}).get("default", {}).get("models", [])
    for i, model in enumerate(default_models):
        print(f"  {i + 1}. {model['model']} (score: {model['score']})")

    if not default_models:
        print("警告: default group 无模型，跳过测试")
        return

    first_model = default_models[0]
    print(f"\n第一个模型: {first_model['model']}")

    print("\n当前时间:", time.strftime("%Y-%m-%d %H:%M:%S"))
    print("\n测试说明:")
    print("1. 当前是第一个模型优先")
    print("2. 待后续观察，若第一个模型出现失败后，其分数会下降")
    print("3. 分数下降后，请求应该优先选择其他高分模型")
    print("4. 如需测试，请手动使用一段时间后，再次运行此脚本")


def main() -> None:
    print("=" * 60)
    print("New API 模型拦截器测试")
    print("=" * 60)
    print(f"API 地址: {BASE_URL}")
    print(f"API Key: {API_KEY[:20]}...")
    print()

    try:
        print("检查 API 连通性...")
        status_response = requests.get(f"{BASE_URL}/api/status", timeout=10)
        if status_response.status_code == 200:
            print("API 连通正常")
        else:
            print(f"API 响应异常: {status_response.status_code}")
            sys.exit(1)
    except Exception as e:
        print(f"API 连接失败: {e}")
        sys.exit(1)

    test_1_failover()

    time.sleep(3)
    test_2_rank_priority()

    print("\n" + "=" * 60)
    print("测试完成")
    print("=" * 60)


if __name__ == "__main__":
    main()
