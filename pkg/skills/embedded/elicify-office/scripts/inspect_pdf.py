#!/usr/bin/env python3
"""Read-only PDF geometry and form inventory; not a rendering/signature verifier."""
import argparse
import json
import os
from pathlib import Path

from pypdf import PdfReader
from pypdf.errors import PdfReadError


def inspect_pdf(path, password=None):
    with Path(path).open('rb') as source:
        try:
            reader = PdfReader(source, strict=True)
            encrypted = reader.is_encrypted
            if encrypted and (password is None or not reader.decrypt(password)):
                raise ValueError('PDF password required or incorrect')
            fields = reader.get_fields() or {}
            return {
                'page_count': len(reader.pages),
                'pages': [{'number': index + 1,
                           'width_points': float(page.mediabox.width),
                           'height_points': float(page.mediabox.height),
                           'rotation': page.rotation}
                          for index, page in enumerate(reader.pages)],
                'fields': sorted(fields),
                'signature_fields': sorted(name for name, field in fields.items()
                                           if field.get('/FT') == '/Sig'),
                'encrypted': encrypted,
            }
        except PdfReadError as exc:
            raise ValueError('invalid PDF') from exc


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('pdf', type=Path)
    parser.add_argument('--password-env', help='Name of environment variable containing the password')
    args = parser.parse_args()
    try:
        password = os.environ[args.password_env] if args.password_env else None
        print(json.dumps(inspect_pdf(args.pdf, password), indent=2))
    except KeyError:
        parser.exit(2, 'PDF inspection failed: password environment variable is unset\n')
    except (ValueError, OSError) as exc:
        parser.exit(2, f'PDF inspection failed: {exc}\n')


if __name__ == '__main__':
    main()
