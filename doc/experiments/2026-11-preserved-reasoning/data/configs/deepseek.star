p = provider("openai", base_url = "https://api.commandcode.ai/provider/v1", api_key = env("COMMAND_CODE_API_KEY"))
default = "m"
models = {"m": model(p, "deepseek/deepseek-v4-flash", context = 131072, reasoning = "low", cache = True, max_output = 32768)}
retry_timeout = 300
