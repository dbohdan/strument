orr = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "mimo"
models = {"mimo": model(orr, "xiaomi/mimo-v2.6-flash", context = 131072, reasoning = "low", cache = True)}
