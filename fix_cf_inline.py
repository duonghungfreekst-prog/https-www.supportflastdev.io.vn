import re

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'r', encoding='utf-8') as f:
    content = f.read()

# Replace login inline wrapper
content = content.replace(
    '<div style="margin-bottom:16px;display:flex;justify-content:center;align-items:center;width:100%;min-height:65px;">',
    '<div style="margin-bottom:16px;display:block;width:100%;height:65px;overflow:hidden;text-align:center;">'
)

content = content.replace(
    '<div id="cf-turnstile-login" class="cf-turnstile-clean" style="display:flex;align-items:center;justify-content:center;"></div>',
    '<div id="cf-turnstile-login" class="cf-turnstile-clean" style="display:inline-block;height:65px;overflow:hidden;"></div>'
)

content = content.replace(
    '<div id="cf-turnstile-register" class="cf-turnstile-clean" style="display:flex;align-items:center;justify-content:center;"></div>',
    '<div id="cf-turnstile-register" class="cf-turnstile-clean" style="display:inline-block;height:65px;overflow:hidden;"></div>'
)

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'w', encoding='utf-8') as f:
    f.write(content)
