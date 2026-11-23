cc = provider("openai", base_url = "https://api.commandcode.ai/provider/v1", api_key = env("COMMAND_CODE_API_KEY"))
default = "mimo"
models = {"mimo": model(cc, "xiaomi/mimo-v2.6-flash", context = 131072, reasoning = "low", cache = True)}
