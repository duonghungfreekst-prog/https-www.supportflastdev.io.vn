import re

# Fix CSS
with open(r'f:\supportflast.dev\supportflast_ui\storage\css\style.css', 'r', encoding='utf-8') as f:
    css_content = f.read()

new_css = '''
.cf-turnstile-clean,
#cf-turnstile-storage-login.cf-turnstile-clean {
    position: relative !important;
    height: 65px !important;
    max-height: 65px !important;
    min-height: 65px !important;
    overflow: hidden !important;
    display: block !important;
    border-radius: 8px !important;
    box-sizing: border-box !important;
}

.cf-turnstile-clean iframe,
.cf-turnstile-clean > div {
    height: 65px !important;
    max-height: 65px !important;
    overflow: hidden !important;
    transform: translateY(0) !important;
}
'''

css_content = re.sub(r'\.cf-turnstile-clean,[\s\S]*?\.cf-turnstile-clean > div > div \{[\s\S]*?\}', new_css.strip(), css_content)

with open(r'f:\supportflast.dev\supportflast_ui\storage\css\style.css', 'w', encoding='utf-8') as f:
    f.write(css_content)

# Fix HTML
with open(r'f:\supportflast.dev\supportflast_ui\storage\index.html', 'r', encoding='utf-8') as f:
    html_content = f.read()

html_content = html_content.replace(
    '<div id="cf-turnstile-storage-login" class="cf-turnstile-clean" style="display: flex; align-items: center; justify-content: center;"></div>',
    '<div style="display:block;width:100%;height:65px;overflow:hidden;text-align:center;"><div id="cf-turnstile-storage-login" class="cf-turnstile-clean" style="display:inline-block;height:65px;overflow:hidden;"></div></div>'
)

with open(r'f:\supportflast.dev\supportflast_ui\storage\index.html', 'w', encoding='utf-8') as f:
    f.write(html_content)

