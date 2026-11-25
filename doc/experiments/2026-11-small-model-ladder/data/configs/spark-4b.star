p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "Spark-X2.6-4B-Q4_K_M.gguf", context = 128000, reasoning = "low", max_output = 32768)}
retry_timeout = 3600
