import React, { useState } from 'react';
import { Card, Form, Switch, Select, Input, Tag, Alert, Button, message } from 'antd';
import { SendOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import API from '../../utils/api';

const { TextArea } = Input;
const api = new API();

interface ConfigFieldProps {
  label: string;
  description?: string;
  children: React.ReactElement;
}

// 简化版 ConfigField 组件
const ConfigField: React.FC<ConfigFieldProps> = ({ label, description, children }) => (
  <div className="config-item" style={{ marginBottom: 16 }}>
    <div className="config-item-label" style={{ marginBottom: 4, fontWeight: 500 }}>{label}</div>
    <div className="config-item-content">
      <div className="config-item-input">{children}</div>
      {description && (
        <div className="config-item-description" style={{ marginTop: 4, color: '#888', fontSize: 12 }}>
          {description}
        </div>
      )}
    </div>
  </div>
);

// B站常见分区
const BILI_TID_OPTIONS = [
  { value: 21, label: '直播' },
  { value: 3, label: '音乐' },
  { value: 17, label: '单机游戏' },
  { value: 65, label: '网络游戏' },
  { value: 171, label: '电子竞技' },
  { value: 172, label: '手机游戏' },
  { value: 160, label: '生活' },
  { value: 138, label: '搞笑' },
  { value: 119, label: '鬼畜' },
  { value: 155, label: '时尚' },
  { value: 5, label: '科技' },
];

interface BiliPublishSettingsProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  config: any;
}

/**
 * B站投稿设置组件
 * 用于 GlobalSettings 中显示 B站投稿配置
 */
const BiliPublishSettings: React.FC<BiliPublishSettingsProps> = ({ config }) => {
  const isEnabled = config.on_record_finished?.bili_publish?.enable;
  const form = Form.useFormInstance();
  const [verifying, setVerifying] = useState(false);
  const [loginInfo, setLoginInfo] = useState<{ success: boolean; uname?: string; mid?: number; message?: string } | null>(null);

  const handleVerifyCookie = async () => {
    const values = form.getFieldsValue();
    const cookie = values.on_record_finished?.bili_publish?.cookie || '';
    if (!cookie.trim()) {
      message.warning('未填写 Cookie，留空则复用全局 Cookie，无需在此验证');
      return;
    }
    setVerifying(true);
    setLoginInfo(null);
    try {
      const rsp: any = await api.verifyBilibiliCookie(cookie.trim());
      if (rsp.code === 0 && rsp.data && rsp.data.isLogin) {
        setLoginInfo({
          success: true,
          uname: rsp.data.uname,
          mid: rsp.data.mid,
        });
        message.success('Cookie 有效');
      } else {
        setLoginInfo({ success: false, message: 'Cookie 未登录或已失效' });
        message.error('Cookie 未登录或已失效');
      }
    } catch (err: any) {
      setLoginInfo({ success: false, message: err.message || String(err) });
      message.error(`验证失败: ${err.message || err}`);
    } finally {
      setVerifying(false);
    }
  };

  return (
    <Card
      title={<><SendOutlined /> B站投稿</>}
      size="small"
      style={{ marginBottom: 16 }}
      extra={
        <Tag color={isEnabled ? 'green' : 'default'}>
          {isEnabled ? '已启用' : '未启用'}
        </Tag>
      }
    >
      <Alert
        message="B站自动投稿功能"
        description={
          <>
            录制完成后自动投稿到 B站。投稿阶段先于云盘上传执行，投稿失败不会阻断上传，但任务会标记为失败。
            <br />
            默认所有直播间都不投稿，需要在“直播间列表”中单独为指定主播开启投稿。
          </>
        }
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <ConfigField
        label="启用 B站投稿"
        description="总开关：开启后，已单独开启投稿的直播间才会在录制完成后自动投稿"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'enable']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="显式 Cookie（可选）"
        description="留空则复用全局 B站 Cookie（设置-直播间 Cookie 中配置）。投稿与录制共用登录态"
      >
        <div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
            <Form.Item name={['on_record_finished', 'bili_publish', 'cookie']} noStyle>
              <TextArea rows={2} placeholder="粘贴完整 Cookie 字符串，包含 SESSDATA 和 bili_jct" style={{ width: 360 }} />
            </Form.Item>
            <Button onClick={handleVerifyCookie} loading={verifying}>验证</Button>
          </div>
          {loginInfo && (
            <div style={{ marginTop: 6, fontSize: 12 }}>
              {loginInfo.success ? (
                <span style={{ color: '#52c41a' }}>
                  <CheckCircleOutlined /> 已登录：{loginInfo.uname}（UID: {loginInfo.mid}）
                </span>
              ) : (
                <span style={{ color: '#ff4d4f' }}>
                  <CloseCircleOutlined /> {loginInfo.message}
                </span>
              )}
            </div>
          )}
        </div>
      </ConfigField>
      <ConfigField
        label="标题模板"
        description='支持变量: {{ .Platform }}, {{ .HostName }}, {{ .RoomName }}, {{ .FileName }}, {{ .Ext }}, {{ .StartTime }}，以及 {{ now | date "2006-01-02" }} 等函数'
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'title_tmpl']} noStyle>
          <Input placeholder='{{ .HostName }} {{ now | date "2006-01-02" }} 直播录像' style={{ width: 400 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="分P标题模板"
        description="多文件投稿（分段录制）时每个分P的标题。额外支持 {{ .Index }}（从 1 开始的分P序号）；留空则所有分P使用主标题"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'p_title_tmpl']} noStyle>
          <Input placeholder='{{ .HostName }} {{ now | date "2006-01-02" }} 第 {{ .Index }} 部分' style={{ width: 400 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="简介模板"
        description="投稿简介，支持与标题相同的模板变量"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'desc_tmpl']} noStyle>
          <TextArea rows={2} placeholder="本视频由 bililive-go 自动录制并投稿。" style={{ width: 400 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="投稿分区 (tid)"
        description="稿件发布到的 B站分区，默认 21（直播）"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'tid']} noStyle>
          <Select
            style={{ width: 250 }}
            showSearch
            optionFilterProp="label"
            placeholder="选择分区"
            options={BILI_TID_OPTIONS}
          />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="标签"
        description="最多 12 个标签，回车添加"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'tags']} noStyle>
          <Select mode="tags" style={{ width: 400 }} placeholder="输入标签后回车" tokenSeparators={[',', '，']} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="使用提取的封面"
        description="开启后使用 extract_cover 阶段生成的封面；未提取到封面或未开启封面提取时投稿不带封面"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'cover_use_extracted']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="定时发布"
        description="留空立即发布。格式: 2026-08-20T10:00:00+08:00（RFC3339）"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'dtime']} noStyle>
          <Input placeholder="2026-08-20T10:00:00+08:00" style={{ width: 300 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="投稿成功后删除本地文件"
        description="投稿成功后才删除视频文件；若同时开启了云上传，云上传阶段会在投稿完成后执行"
      >
        <Form.Item name={['on_record_finished', 'bili_publish', 'delete_after']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
    </Card>
  );
};

export default BiliPublishSettings;
