orr = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "hy4f"
models = {"hy4f": model(orr, "tencent/hy4-flash", context = 131072, reasoning = "low")}
