p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "XHToken/Spark-X2.6-4B-GGUF:Q4_K_M", context = 65536, reasoning = "low",
    temperature = 0.6, max_output = 32768,
    extra_params = {"top_p": 0.95, "top_k": 20})}
retry_timeout = 3600
