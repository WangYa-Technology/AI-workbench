#!/usr/bin/env python3
"""Seed eight explicitly labelled purchases in the fixed LOCAL development DB.

Usage: python3 scripts/seed-purchase-examples.py --buyer-id UUID
Requires Pillow, ffmpeg, and the hcai-chat-postgres-1 Docker container.
Creates zero-dollar test orders, frozen license/source evidence and local files.
Does not contact a gateway, change balances, or replace historical order records.
Creates active local example listings with separate previews so order links work.
Reruns repair draft listings created by earlier versions of this fixture only.
"""
import argparse
import json
import math
from pathlib import Path
import runpy
import struct
import subprocess
import uuid
import wave

ROOT = Path(__file__).resolve().parents[1]
helpers = runpy.run_path(str(ROOT / 'scripts/seed-market-examples.py'))
psql, sql, cover = (helpers[key] for key in ('psql', 'sql', 'cover'))
NAMESPACE = uuid.UUID('b83d822b-4aeb-5f12-827a-67b09b25d6a2')
ITEMS = [
    ('image', '晨光静物 · 品牌背景', 'MORNING / OBJECTS', 'sage', 'hcai-commercial-standard-v1', 'clean', 'fulfilled'),
    ('image', '夜色声场 · 专辑封面', 'NIGHT / FREQUENCY', 'night', 'hcai-personal-v1', 'clean', 'fulfilled'),
    ('image', '山海旅记 · 编辑配图', 'OPEN / HORIZONS', 'sand', 'hcai-commercial-standard-v1', 'clean', 'fulfilled'),
    ('video', '蓝调序章 · 片头动态', 'BLUE / INTRO', 'blue', 'hcai-commercial-standard-v1', 'clean', 'fulfilled'),
    ('audio', '轻声提示 · 交互音效', 'QUIET / SIGNAL', 'sage', 'hcai-personal-v1', 'clean', 'fulfilled'),
    ('document', '产品摄影 · 提示词手册', 'LIGHT / NOTES', 'sand', 'hcai-personal-v1', 'clean', 'fulfilled'),
    ('document', '系列视觉 · 交付检查清单', 'VISUAL / CHECKLIST', 'blue', 'hcai-commercial-standard-v1', 'clean', 'refund_requested'),
    ('image', '轨道之间 · 待复核海报', 'ORBIT / REVIEW', 'night', 'hcai-commercial-standard-v1', 'review', 'fulfilled'),
]


def uid(key):
    return str(uuid.uuid5(NAMESPACE, key))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--buyer-id', required=True, type=uuid.UUID)
    args = parser.parse_args()
    buyer = str(args.buyer_id)
    if psql('SELECT current_database();').strip() != 'hcai':
        raise SystemExit('Unexpected development database')
    if psql(f"SELECT count(*) FROM users WHERE id={sql(buyer)} AND status='active';").strip() != '1':
        raise SystemExit('Buyer must be an existing active local account')
    media = ROOT / 'data/media'
    media.mkdir(parents=True, exist_ok=True)
    seller = uid('seller')
    statements = ['BEGIN;', "SET LOCAL app.asset_scan_execution_protocol='lease-v1';", "SET LOCAL app.product_bundle_protocol='zip-v1';", "SET LOCAL app.upload_command_protocol='command-v1';", "SET LOCAL app.upload_write_protocol='journal-v1';"]
    statements.append(f"INSERT INTO users(id,email,handle,display_name,role,status,locale,timezone) VALUES({sql(seller)},'purchase_examples@example.invalid','example_purchase_studio','示例 · 素材档案室','creator','active','zh-CN','Asia/Shanghai') ON CONFLICT(id) DO NOTHING;")
    ids = []
    for i, (kind, title, english, palette, license_code, scan, status) in enumerate(ITEMS):
        source, product, preview = uid(f'source-{i}'), uid(f'product-{i}'), uid(f'preview-{i}')
        order, purchased = uid(f'{buyer}-order-{i}'), uid(f'{buyer}-asset-{i}')
        ids.append(purchased)
        title = '[示例已购] ' + title
        suffix, mime = {'image': ('.png', 'image/png'), 'video': ('.mp4', 'video/mp4'), 'audio': ('.wav', 'audio/wav'), 'document': ('.txt', 'text/plain')}[kind]
        key = source + suffix
        path = media / key
        if not path.exists():
            if kind == 'image':
                cover([0,4,3,2,1,5,2,4][i], english, palette).save(path)
            elif kind == 'video':
                poster = media / (source + '-poster.png')
                cover(2, english, palette).save(poster)
                subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-loop', '1', '-i', str(poster), '-vf', 'scale=960:600,fade=t=in:st=0:d=0.6', '-t', '3', '-r', '24', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', str(path)], check=True)
            elif kind == 'audio':
                rate, duration = 22050, 3
                with wave.open(str(path), 'wb') as wav:
                    wav.setparams((1, 2, rate, 0, 'NONE', 'not compressed'))
                    values = []
                    for n in range(rate * duration):
                        time = n / rate
                        local = time % 1
                        envelope = math.sin(math.pi * min(local / .4, 1)) ** 2 if local < .4 else 0
                        value = int(2800 * envelope * math.sin(2 * math.pi * [440, 554.37, 659.25][int(time)] * time))
                        values.append(struct.pack('<h', value))
                    wav.writeframes(b''.join(values))
            else:
                body = ('主体：一只透明玻璃香水瓶。柔和侧光、浅灰背景，保留材质反射；使用中景构图并预留文字空间。\n\n变体：分别调整主体、光线方向、背景色；逐项检查轮廓、材质与画面清晰度。' if i == 5 else '1. 明确交付尺寸、格式和色彩空间。\n2. 核对系列色彩、字体、边距与命名。\n3. 检查素材来源与授权范围。\n4. 导出预览图和最终文件。\n5. 核对交付清单并归档版本。')
                path.write_text(f'{title}\n\n{body}\n\n本地界面演示内容，由代码和人工文本构造，未调用生成模型。测试订单金额为零，没有真实扣款或结算。\n', encoding='utf-8')
        # A public sample must have its own asset/storage key, never the delivery root.
        preview_key = preview + '.png'
        preview_path = media / preview_key
        if not preview_path.exists():
            cover([0,4,3,2,1,5,2,4][i], english, palette).resize((800, 500)).save(preview_path)
        width, height = ('1200','750') if kind == 'image' else ('960','600') if kind == 'video' else ('NULL','NULL')
        when = f"now()-interval '{i+1} days'"
        statements.append(f"INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code,storage_backend,storage_key,size_bytes,scanned_at,created_at) VALUES({sql(source)},{sql(seller)},{sql(kind)},{sql(title)},{sql('/api/v1/assets/'+source+'/content')},{sql(mime)},{width},{height},'clean','upload',{sql(license_code)},'local_file',{sql(key)},{path.stat().st_size},now(),{when}) ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code,storage_backend,storage_key,size_bytes,scanned_at) VALUES({sql(preview)},{sql(seller)},'image',{sql(title+' · 预览')},{sql('/api/v1/assets/'+preview+'/content')},'image/png',800,500,'clean','upload',{sql(license_code)},'local_file',{sql(preview_key)},{preview_path.stat().st_size},now()) ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO products(id,seller_id,asset_id,preview_asset_id,title,description,product_type,category,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility) VALUES({sql(product)},{sql(seller)},{sql(source)},{sql(preview)},{sql(title)},'本地已购内容界面演示；零金额测试权益，无真实扣款。','asset','market_asset',0,'USD',{sql(license_code)},'active','代码绘制和人工编写的开发示例；未使用生成模型。',{sql(json.dumps([title+suffix],ensure_ascii=False))}::jsonb,{sql(mime)}) ON CONFLICT(id) DO NOTHING;")
        # Repair only the exact generated legacy listings, not paused/managed products.
        statements.append(f"UPDATE products SET status='active',preview_asset_id={sql(preview)} WHERE id={sql(product)} AND seller_id={sql(seller)} AND asset_id={sql(source)} AND title={sql(title)} AND price_cents=0 AND status IN ('draft','active') AND (preview_asset_id IS NULL OR preview_asset_id={sql(preview)}) AND NOT EXISTS(SELECT 1 FROM product_publications WHERE product_id=products.id) AND (status<>'active' OR preview_asset_id IS DISTINCT FROM {sql(preview)}::uuid);")
        statements.append(f"INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,created_at,refund_requested_at,refund_reason) SELECT {sql(order)},{sql(buyer)},p.id,0,'USD',{sql(status)},{when},{sql('local-purchase-example-'+order)},p.title,l.name,l.version,l.terms,l.refund_window_days,{when},{'now()' if status == 'refund_requested' else 'NULL'},{sql('本地示例：展示退款处理中状态，无真实资金。' if status == 'refund_requested' else '')} FROM products p JOIN licenses l ON l.code=p.license_code WHERE p.id={sql(product)} ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract,accepted_at) SELECT {sql(order)},source_asset_id,root_asset_id,offer_version,contract,{when} FROM product_offers WHERE product_id={sql(product)} ON CONFLICT(order_id) DO NOTHING;")
        statements.append(f"INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,scan_reason,source_type,source_id,origin_asset_id,license_code,size_bytes,created_at) VALUES({sql(purchased)},{sql(buyer)},{sql(kind)},{sql(title)},{sql('/api/v1/assets/'+purchased+'/content')},{sql(mime)},{width},{height},{sql(scan)},{sql('本地示例：待人工复核状态。' if scan == 'review' else '本地示例内容。')},'purchase',{sql(order)},{sql(source)},{sql(license_code)},{path.stat().st_size},{when}) ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO entitlements(id,user_id,product_id,order_id,asset_id,license_code,status,granted_at) VALUES({sql(uid(order+'entitlement'))},{sql(buyer)},{sql(product)},{sql(order)},{sql(purchased)},{sql(license_code)},'active',{when}) ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,request_id,reason,metadata) VALUES({sql(uid(order+'audit'))},{sql(buyer)},'marketplace.test_purchase','order',{sql(order)},'local-purchase-examples','Explicit local zero-dollar UI demonstration; no charge or payout.',jsonb_build_object('productId',{sql(product)},'realCharge',false,'paymentMode','test','fixture','purchase-examples')) ON CONFLICT(id) DO NOTHING;")
        statements.append(f"INSERT INTO order_events(id,order_id,actor_id,from_status,to_status,reason,sequence,created_at) VALUES({sql(uid(order+'event'))},{sql(order)},{sql(buyer)},NULL,{sql(status)},'本地零金额示例，仅展示已购内容与授权状态。',1,{when}) ON CONFLICT(id) DO NOTHING;")
    statements.append('COMMIT;')
    psql('\n'.join(statements))
    public_count = int(psql('SELECT count(*) FROM public_products p JOIN public_product_previews preview ON preview.product_id=p.id WHERE p.id IN (' + ','.join(sql(uid(f'product-{i}')) for i in range(len(ITEMS))) + ');').strip())
    if public_count != len(ITEMS):
        raise SystemExit(f'Only {public_count}/{len(ITEMS)} example listings are visible. Existing non-fixture changes were preserved.')
    print(json.dumps({'buyerId': buyer, 'assets': ids, 'count': len(ids), 'publicProducts': public_count, 'realCharge': False}, ensure_ascii=False))


if __name__ == '__main__':
    main()
