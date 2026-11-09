#!/usr/bin/env python3
"""Send one test mail through MailSender's SMTP ingress.

    python3 deploy/smtp-test.py mail.example.com you@example.com [--port 465|587]

The API key is read from the MS_KEY environment variable or prompted for (not echoed).
"""
import argparse
import getpass
import os
import smtplib
import ssl
import sys
import time
from email.message import EmailMessage


def tls_context() -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    # python.org builds on macOS ship without system roots unless "Install Certificates" was run
    if not ctx.get_ca_certs() and os.path.exists("/etc/ssl/cert.pem"):
        ctx = ssl.create_default_context(cafile="/etc/ssl/cert.pem")
    return ctx


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("host", help="MailSender host, e.g. mail.example.com")
    ap.add_argument("to", help="recipient address")
    ap.add_argument("--port", type=int, default=465, help="465 = SSL/TLS (default), 587 = STARTTLS")
    args = ap.parse_args()

    key = os.environ.get("MS_KEY") or getpass.getpass("API key (ms_...): ").strip()
    if not key:
        print("no API key given", file=sys.stderr)
        return 2

    stamp = time.strftime("%Y-%m-%d %H:%M:%S")
    msg = EmailMessage()
    msg["From"] = "MailSender 测试 <test@example.com>"  # replaced by the real account; kept as display name + Reply-To
    msg["To"] = args.to
    msg["Subject"] = f"MailSender SMTP 测试 {stamp}"
    msg.set_content(f"这是一封通过 MailSender SMTP 接入发送的测试邮件。\n发送时间：{stamp}")
    msg.add_alternative(
        f"<p>这是一封通过 <b>MailSender</b> SMTP 接入发送的测试邮件。</p><p>发送时间：{stamp}</p>", subtype="html"
    )

    ctx = tls_context()
    t0 = time.time()
    try:
        if args.port == 465:
            s = smtplib.SMTP_SSL(args.host, args.port, context=ctx, timeout=120)
        else:
            s = smtplib.SMTP(args.host, args.port, timeout=120)
            s.starttls(context=ctx)
        with s:
            s.login("apikey", key)
            refused = s.send_message(msg)
    except smtplib.SMTPResponseException as e:
        print(f"失败：{e.smtp_code} {e.smtp_error.decode(errors='replace')}")
        return 1
    except smtplib.SMTPRecipientsRefused as e:
        for rcpt, (code, text) in e.recipients.items():
            print(f"收件人被拒绝：{rcpt} {code} {text.decode(errors='replace')}")
        return 1
    except Exception as e:  # network / TLS problems
        print(f"失败：{type(e).__name__}: {e}")
        return 1
    if refused:
        print(f"部分收件人被拒绝：{refused}")
        return 1
    print(f"成功：MailSender 已接受并发出（{time.time() - t0:.1f}s）。请到 {args.to} 收件箱查看，也检查一下垃圾邮件文件夹。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
