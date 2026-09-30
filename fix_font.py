import re

with open(r'f:\supportflast.dev\supportflast_ui\share.html', 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace(
    'family=Plus+Jakarta+Sans:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap',
    'family=Be+Vietnam+Pro:ital,wght@0,400;0,500;0,600;0,700;0,800;1,400;1,700&family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap'
)

content = content.replace(
    "font-family: 'Plus Jakarta Sans', sans-serif;",
    "font-family: 'Be Vietnam Pro', 'Inter', sans-serif;"
)

with open(r'f:\supportflast.dev\supportflast_ui\share.html', 'w', encoding='utf-8') as f:
    f.write(content)
