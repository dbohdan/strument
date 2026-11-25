p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "XHToken/Spark-X2.6-4B-GGUF:Q4_K_M", context = 32768, reasoning = "low", max_output = 20480)}
retry_timeout = 3600
