#!/usr/bin/env python3
import requests
import time
import json
import sys

BASE_URL = "http://localhost:3000"
API_KEY = "sk-XENzRnIqeYEApfilOmJCYN5rxQ4bnQN6pQ0VHmYtR7wgi6QN"

def get_rank_status():
    url = f"{BASE_URL}/api/model_rank/status"
    response = requests.get(url)
    return response.json()

def print_rank(models, title):
    print(f"\n{title}:")
    for m in models:
        print(f"  - {m['model']}")
        print(f"    Score: {m['score']}, Success: {m['successes']}, Failure: {m['failures']}")

def main():
    print("=" * 60)
    print("测试1：获取初始排名状态")
    print("=" * 60)
    
    # 测试初始状态
    status = get_rank_status()
    if not status.get("success"):
        print("获取排名失败")
        sys.exit(1)
    
    data = status.get("data", {})
    default_models = data.get("default", {}).get("models", [])
    print_rank(default_models, "初始 Default 分组模型")
    
    print("\n" + "=" * 60)
    print("测试2：测试增删接口")
    print("=" * 60)
    
    # 添加模型
    add_response = requests.post(
        f"{BASE_URL}/api/model_rank/add",
        json={"category": "default", "model": "test-added-model-1"}
    )
    print("\n添加模型:")
    print(json.dumps(add_response.json(), ensure_ascii=False, indent=2))
    
    # 再次查看状态
    new_status = get_rank_status()
    new_default = new_status.get("data", {}).get("default", {}).get("models", [])
    print_rank(new_default, "添加后 Default 分组模型")
    
    # 删除模型
    remove_response = requests.post(
        f"{BASE_URL}/api/model_rank/remove",
        json={"category": "default", "model": "test-added-model-1"}
    )
    print("\n删除模型:")
    print(json.dumps(remove_response.json(), ensure_ascii=False, indent=2))
    
    final_status = get_rank_status()
    final_default = final_status.get("data", {}).get("default", {}).get("models", [])
    print_rank(final_default, "删除后 Default 分组模型")
    
    print("\n" + "=" * 60)
    print("测试3：验证拦截器逻辑")
    print("=" * 60)
    
    # 测试请求不会被拦截的模型（因为没有匹配）
    print("\n测试请求未匹配的模型（应该透传）：")
    try:
        test_model = "some-random-model-not-in-config"
        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {API_KEY}"
        }
        payload = {
            "model": test_model,
            "messages": [{"role": "user", "content": "hi"}]
        }
        response = requests.post(
            f"{BASE_URL}/v1/chat/completions",
            json=payload,
            headers=headers,
            timeout=5
        )
        print(f"未匹配模型请求状态码: {response.status_code}")
    except Exception as e:
        print(f"请求异常: {e}")
    
    # 检查日志（这里只提示）
    print("\n✅ 测试提示：")
    print("- 请在 Docker 日志中查看拦截器日志")
    print("- 如果看到 [ModelInterceptor] 模型替换 则说明拦截生效")
    print("- 如果没有替换说明该模型没有匹配到任何分组（正确行为）")
    
    print("\n" + "=" * 60)
    print("所有测试完成！")
    print("=" * 60)

if __name__ == "__main__":
    main()

