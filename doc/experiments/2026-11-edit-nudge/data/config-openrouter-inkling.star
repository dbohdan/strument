orr = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "inkling"
models = {"inkling": model(orr, "thinkingmachines/inkling-2-mini", context = 131072, reasoning = "low", cache = True)}
