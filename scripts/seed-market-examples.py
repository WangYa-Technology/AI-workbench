#!/usr/bin/env python3
"""Seed 12 local resource-market examples. Requires Pillow and the dev Docker DB.
Usage: python3 scripts/seed-market-examples.py [--media-root data/media]
Run from the repository root. Existing sample IDs are preserved on subsequent runs.
Uses the fixed hcai-chat-postgres-1 development container and hcai database.
Four categories receive three resources each; previews and delivery files are separate.
No credentials, orders, entitlements or payment configuration are created/modified.
"""
import argparse
import json
from pathlib import Path
import subprocess
import uuid
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
NAMESPACE = uuid.UUID('b6d439f4-0179-4e38-b60a-5b1e2ef19021')
ITEMS = [
    ('asset', '静物之间 · 产品摄影背景', 'STILL / LIFE', '低饱和几何构图，适合香氛、护肤品和独立品牌的静物展示。', 1800, 'commercial', 'sage'),
    ('prompt', '电影感人像 · 光线提示词', 'PORTRAIT / LIGHT', '以主光、轮廓光与背景光组织人像描述，附可替换参数与负面约束。', 900, 'personal', 'night'),
    ('workflow', '从灵感到海报 · 创作流程', 'IDEA TO / POSTER', '梳理选题、视觉探索、排版和导出四个阶段，适合品牌海报练习。', 2900, 'commercial', 'blue'),
    ('work', '山海之间 · 旅行视觉', 'LAND / ESCAPE', '用抽象山脉与留白表达远行主题，可用于编辑配图与封面布局练习。', 3600, 'commercial', 'sand'),
    ('asset', '声波形状 · 音乐封面素材', 'SOUND / FORMS', '由同心圆和节奏线条构成的抽象图形，适合播客与电子音乐封面。', 1600, 'commercial', 'night'),
    ('prompt', '微缩世界 · 场景提示词', 'SMALL / WORLDS', '拆解主体比例、材质、镜头与环境叙事，构建连贯的微缩场景描述。', 1200, 'personal', 'sage'),
    ('workflow', '电商主图 · 视觉检查流程', 'PRODUCT / SYSTEM', '包含主体检查、背景选择、信息层级与多比例适配的结构化清单。', 3900, 'commercial', 'sand'),
    ('work', '夜行城市 · 编辑插画', 'CITY / AFTER DARK', '深色块面与建筑节奏形成城市夜景，适合文化刊物和故事专题。', 4800, 'commercial', 'blue'),
    ('asset', '阅读时刻 · 几何图形素材', 'READ / SLOWLY', '书页、窗格与纸张比例组成的简洁图形，可作为阅读主题视觉参考。', 1400, 'personal', 'blue'),
    ('prompt', '自然材质 · 包装提示词', 'NATURAL / MATERIALS', '围绕纸张、玻璃与织物组织材质描述，附光线、配色与构图建议。', 800, 'personal', 'sand'),
    ('workflow', '系列封面 · 一致性工作流', 'ONE / VISUAL SYSTEM', '从视觉规则出发，通过配色、边距、字体层级和检查点保持系列一致。', 3200, 'commercial', 'sage'),
    ('work', '轨道之间 · 抽象海报', 'ORBIT / STUDIES', '通过轨道与切片构建有秩序的抽象画面，适合展览视觉概念练习。', 4200, 'commercial', 'night'),
]
PALETTES = {'sage': ('#e7ebdf', '#283e36', '#a6b69a'), 'night': ('#172135', '#e3e8ef', '#6e8ba1'), 'blue': ('#e8edf5', '#294463', '#99b3d0'), 'sand': ('#eeeae4', '#494c4a', '#b2b5b0')}


def uid(key): return str(uuid.uuid5(NAMESPACE, key))
def sql(value): return "'" + str(value).replace("'", "''") + "'"
def psql(query):
    result = subprocess.run(['docker', 'exec', '-i', 'hcai-chat-postgres-1', 'psql', '-X', '-U', 'hcai', '-d', 'hcai', '-v', 'ON_ERROR_STOP=1', '-At'], input=query, text=True, capture_output=True)
    if result.returncode: raise SystemExit(result.stderr.strip())
    return result.stdout


def font(size):
    for path in ['/System/Library/Fonts/Supplemental/Arial.ttf', '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf']:
        if Path(path).exists(): return ImageFont.truetype(path, size)
    return ImageFont.load_default(size=size)


def cover(index, title, palette):
    bg, ink, accent = PALETTES[palette]
    image = Image.new('RGB', (1200, 750), bg)
    d = ImageDraw.Draw(image)
    d.text((55, 42), 'HCAI   /   SAMPLE RESOURCE', font=font(19), fill=ink)
    d.text((1080, 42), f'{index + 1:02}', font=font(22), fill=ink)
    mode = index % 6
    if mode == 0:
        d.ellipse((610, 135, 1080, 605), fill=accent)
        d.rectangle((565, 450, 1130, 640), fill=ink)
        d.rounded_rectangle((740, 240, 885, 510), 22, fill=bg, outline=ink, width=5)
        d.rectangle((775, 205, 850, 242), fill=ink)
    elif mode == 1:
        for i in range(9):
            x = 615 + i * 35
            d.rounded_rectangle((x, 130 + i * 12, x + 32, 630 - i * 16), 16, fill=accent if i % 2 else ink)
        d.ellipse((780, 195, 1030, 445), fill=bg)
    elif mode == 2:
        for i in range(4):
            x, y = 630 + (i % 2) * 225, 165 + (i // 2) * 230
            d.rounded_rectangle((x, y, x + 192, y + 192), 22, fill=accent if i % 3 else ink)
            d.text((x + 26, y + 25), f'0{i+1}', font=font(34), fill=bg)
            d.line((x + 28, y + 135, x + 152, y + 135), fill=bg, width=3)
    elif mode == 3:
        d.ellipse((920, 120, 1080, 280), fill=accent)
        for i in range(3):
            y = 310 + i * 105
            d.polygon([(560, 640), (560, y + 90), (760, y - 100), (900, y + 30), (1060, y - 80), (1140, y + 30), (1140, 640)], fill=ink if i % 2 else accent)
    elif mode == 4:
        for i in range(7):
            r = 245 - i * 28
            d.ellipse((865-r, 380-r, 865+r, 380+r), outline=ink if i % 2 else accent, width=12)
        d.rectangle((555, 350, 1160, 410), fill=bg)
    else:
        for i in range(6):
            x, y = 610 + (i % 3) * 170, 200 + (i // 3) * 205
            d.polygon([(x, y+50),(x+70,y),(x+140,y+50),(x+70,y+100)], fill=accent)
            d.polygon([(x,y+50),(x+70,y+100),(x+70,y+160),(x,y+110)], fill=ink)
            d.line((x+70,y+100,x+140,y+50,x+140,y+110,x+70,y+160), fill=ink, width=3)
    for i, line in enumerate(title.split(' / ')):
        d.text((55, 205 + i * 68), line, font=font(49), fill=ink)
    d.text((55, 635), 'DESIGN PREVIEW  /  LOCAL EXAMPLE', font=font(16), fill=ink)
    return image


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--media-root', type=Path, default=ROOT / 'data/media')
    args = parser.parse_args()
    media = args.media_root.resolve()
    if not media.is_relative_to(ROOT): raise SystemExit('Example media must remain inside this local repository.')
    # Fixed development container and database; never accepts a remote database URL.
    if psql('SELECT current_database();').strip() != 'hcai': raise SystemExit('Unexpected development database')
    media.mkdir(parents=True, exist_ok=True)
    # Match the asset writer protocol used by database.Open for locally generated fixtures.
    statements = ['BEGIN;', "SET LOCAL app.asset_scan_execution_protocol='lease-v1';", "SET LOCAL app.product_bundle_protocol='zip-v1';", "SET LOCAL app.upload_command_protocol='command-v1';", "SET LOCAL app.upload_write_protocol='journal-v1';"]
    sellers = [('example_still_studio', '示例 · 留白工作室'), ('example_prompt_lab', '示例 · 提示词实验室'), ('example_form_studio', '示例 · 形状工坊')]
    for handle, name in sellers:
        statements.append(f"INSERT INTO users(id,email,handle,display_name,role,status,locale,timezone) VALUES({sql(uid(handle))},{sql(handle+'@example.invalid')},{sql(handle)},{sql(name)},'creator','active','zh-CN','Asia/Shanghai') ON CONFLICT (id) DO NOTHING;")
    for i, (kind, title, english, summary, price, rights, palette) in enumerate(ITEMS):
        product, source, preview = (uid(f'{key}-{i}') for key in ['product', 'source', 'preview'])
        seller = uid(sellers[i % 3][0])
        license_code = 'hcai-commercial-standard-v1' if rights == 'commercial' else 'hcai-personal-v1'
        image = cover(i, english, palette)
        preview_key = preview + '.png'
        if not (media / preview_key).exists(): image.save(media / preview_key)
        if kind in ('asset', 'work'):
            suffix, asset_kind, mime = '.png', 'image', 'image/png'
            source_key = source + suffix
            if not (media / source_key).exists(): image.save(media / source_key)
            compatibility = 'PNG · 1200 × 750 px；本地示例图形，可在图片编辑器中查看。'
        else:
            suffix, asset_kind, mime = '.txt', kind, 'text/plain'
            source_key = source + suffix
            body = f'# {title}（本地示例）\n\n{summary}\n\n'
            if kind == 'prompt':
                body += '主提示词：以 [主体] 为中心，使用 [材质] 和 [环境] 表达 [主题]。画面采用明确的视觉重心，预留标题空间；通过柔和侧光突出纹理，控制背景细节。\n\n可替换参数：主体、用途、场景、镜头距离、色彩比例、光线方向。\n负面约束：避免杂乱背景、无关水印、变形文字与过强锐化。\n验证：分别改变一个参数，对比主体可读性、构图与色彩。\n'
            else:
                body += '1. 明确用途：记录受众、尺寸、交付格式和使用限制。\n2. 收集参考：选择三种构图，提取色彩、空间和层级规则。\n3. 探索方案：保持主体不变，分别尝试光线、背景和裁切。\n4. 一致性检查：检查系列的边距、字体层级与色彩比例。\n5. 输出检查：预览原尺寸及缩略图，确认阅读顺序与清晰度。\n\n本文件是人工执行的流程清单，不是可导入特定软件的节点工作流。\n'
            body += '\n仅为本地开发和界面演示编写，未调用生成模型；示例价格不代表真实报价。\n'
            if not (media / source_key).exists(): (media / source_key).write_text(body)
            compatibility = 'UTF-8 纯文本；' + ('可复制并改写提示词，效果随模型变化。' if kind == 'prompt' else '人工执行的创作流程清单，不是软件节点文件。')
        for aid, key, akind, amime in [(source, source_key, asset_kind, mime), (preview, preview_key, 'image', 'image/png')]:
            dims = '1200,750' if akind == 'image' else 'NULL,NULL'
            statements.append(f"INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code,storage_backend,storage_key,size_bytes,scanned_at) VALUES({sql(aid)},{sql(seller)},{sql(akind)},{sql('[示例] '+title)},{sql('/api/v1/assets/'+aid+'/content')},{sql(amime)},{dims},'clean','upload',{sql(license_code)},'local_file',{sql(key)},{(media/key).stat().st_size},now()) ON CONFLICT (id) DO NOTHING;")
        statements.append(f"INSERT INTO products(id,seller_id,asset_id,preview_asset_id,title,description,product_type,category,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility,created_at) VALUES({sql(product)},{sql(seller)},{sql(source)},{sql(preview)},{sql('[示例] '+title)},{sql(summary+' 本地开发示例，用于展示资源信息与授权层级。')},{sql(kind)},{sql('market_'+kind)},{price},'USD',{sql(license_code)},'active',{sql('本地开发示例。封面由代码绘制；提示词与流程为演示文本，未使用生成模型。示例价格仅用于界面展示。')},{sql(json.dumps([title+suffix],ensure_ascii=False))}::jsonb,{sql(compatibility)},now()-interval '{i} hours') ON CONFLICT (id) DO NOTHING;")
    statements.append('COMMIT;')
    psql('\n'.join(statements))
    count = psql('SELECT count(*) FROM public_products WHERE id IN ('+','.join(sql(uid(f'product-{i}')) for i in range(len(ITEMS)))+');').strip()
    print(f'{count} example resources visible in local marketplace; media: {media}')

if __name__ == '__main__': main()
