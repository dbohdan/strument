p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "liquid/lfm-2.5-24b-a2b", context = 128000, reasoning = "low", cache = True, max_output = 32768)}
