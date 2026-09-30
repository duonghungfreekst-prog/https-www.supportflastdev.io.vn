with open(r'f:\supportflast.dev\supportflast_core\Cargo.toml', 'a', encoding='utf-8') as f:
    f.write('\n[profile.release]\nopt-level = 3\nlto = \"fat\"\ncodegen-units = 1\npanic = \"abort\"\nstrip = true\n')
