import re

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'r', encoding='utf-8') as f:
    content = f.read()

css = '''
        .cf-turnstile-clean,
        #cf-turnstile-login.cf-turnstile-clean,
        #cf-turnstile-register.cf-turnstile-clean,
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

# We will replace the old cf-turnstile-clean block
old_block_regex = r'\.cf-turnstile-clean,[\s\S]*?\.cf-turnstile-clean > div > div \{[\s\S]*?\}'
content = re.sub(old_block_regex, css.strip(), content)

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'w', encoding='utf-8') as f:
    f.write(content)
