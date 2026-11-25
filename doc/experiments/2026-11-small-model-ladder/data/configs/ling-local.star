p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "bloomer010/Ling-3.0-tiny-GGUF:Q4_K_M", context = 131072, reasoning = "low", max_output = 32768)}
retry_timeout = 3600
