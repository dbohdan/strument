openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

models = {
    "mimo": model(
        openrouter,
        "xiaomi/mimo-v2.5",
        display_name="MiMo V2.5",
        context=262144,
        max_output=16384,
        input_cost=0.14,
        output_cost=0.28,
    ),
    "glm": model(
        openrouter,
        "z-ai/glm-5.3",
        display_name="GLM 5.3",
        context=1048576,
        max_output=16384,
        input_cost=1.4,
        output_cost=4.4,
        reasoning="low",
    ),
    "kimi": model(
        openrouter,
        "moonshotai/kimi-k3",
        display_name="Kimi K3",
        context=1048576,
        max_output=16384,
        input_cost=3.0,
        output_cost=15.0,
        reasoning="low",
    ),
}
default = "mimo"
