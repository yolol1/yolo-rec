import React, { useState } from 'react';
import { Card, Form, Switch, Select, Input, InputNumber, Tag, Alert, Button, Modal, Space } from 'antd';
import { CloudUploadOutlined, PlusOutlined, MinusCircleOutlined, CheckCircleOutlined, CloseCircleOutlined, ApiOutlined } from '@ant-design/icons';
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

interface CloudUploadSettingsProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  config: any;
}

/**
 * 云盘上传设置组件
 * 用于 GlobalSettings 中显示云上传配置
 */
const CloudUploadSettings: React.FC<CloudUploadSettingsProps> = ({ config }) => {
  const isEnabled = config.on_record_finished?.cloud_upload?.enable;
  const form = Form.useFormInstance();
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; steps: Array<{ name: string; success: boolean; message: string }> } | null>(null);

  const handleTestConnection = async () => {
    const values = form.getFieldsValue();
    const cloudConfig = values.on_record_finished?.cloud_upload || {};

    if (!cloudConfig.api_url) {
      Modal.warning({ title: '提示', content: '请先填写 API 地址' });
      return;
    }
    if (!cloudConfig.username) {
      Modal.warning({ title: '提示', content: '请先填写用户名' });
      return;
    }
    if (!cloudConfig.password) {
      Modal.warning({ title: '提示', content: '请先填写密码' });
      return;
    }
    if (!cloudConfig.storage_name) {
      Modal.warning({ title: '提示', content: '请先填写存储名称' });
      return;
    }

    setTesting(true);
    setTestResult(null);
    try {
      const result = await api.testCloudUploadConnection({
        api_url: cloudConfig.api_url,
        username: cloudConfig.username,
        password: cloudConfig.password,
        storage_name: cloudConfig.storage_name,
        additional_storages: cloudConfig.additional_storages || [],
        upload_path_tmpl: cloudConfig.upload_path_tmpl || '',
        delete_after_upload: cloudConfig.delete_after_upload || false,
      });
      setTestResult(result);
    } catch (err: any) {
      setTestResult({
        success: false,
        steps: [{ name: '请求失败', success: false, message: err.message || String(err) }],
      });
    } finally {
      setTesting(false);
    }
  };

  return (
    <Card
      title={<><CloudUploadOutlined /> 云盘上传</>}
      size="small"
      style={{ marginBottom: 16 }}
      extra={
        <Tag color={isEnabled ? 'green' : 'default'}>
          {isEnabled ? '已启用' : '未启用'}
        </Tag>
      }
    >
      <Alert
        message="云盘自动上传功能"
        description={
          <>
            录制完成后自动上传到网盘。需要先在{' '}
            <a href="/remotetools/tool/openlist/" target="_blank" rel="noopener noreferrer">
              OpenList 管理页面
            </a>{' '}
            配置网盘存储。
          </>
        }
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <ConfigField
        label="启用云上传"
        description="开启后录制完成的视频会自动上传到配置的网盘"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'enable']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传时机"
        description="选择何时开始上传：立即上传原始文件，或等待后处理（修复/转码）完成后上传"
      >
        <Form.Item name={['on_record_finished', 'upload_timing']} noStyle>
          <Select style={{ width: 250 }} placeholder="选择上传时机">
            <Select.Option value="">使用默认（立即）</Select.Option>
            <Select.Option value="immediate">立即上传原始文件</Select.Option>
            <Select.Option value="after_process">后处理完成后上传</Select.Option>
          </Select>
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="OpenList API 地址"
        description="外部 OpenList 实例的访问地址，例如：http://192.168.1.100:5244"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'api_url']} noStyle>
          <Input placeholder="http://127.0.0.1:5244" style={{ width: 300 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="OpenList 用户名"
        description="用于登录 OpenList 的管理员账号"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'username']} noStyle>
          <Input placeholder="输入用户名" style={{ width: 300 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="OpenList 密码"
        description="用于登录 OpenList 的管理员密码"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'password']} noStyle>
          <Input.Password placeholder="输入密码" style={{ width: 300 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="存储名称"
        description="在 OpenList 中配置的存储名称，例如：115、阿里云盘"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'storage_name']} noStyle>
          <Input placeholder="例如: 115" style={{ width: 200 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="额外存储"
        description="同时上传到多个存储目标，每个存储名称单独填写（可选）"
      >
        <Form.List name={['on_record_finished', 'cloud_upload', 'additional_storages']}>
          {(fields, { add, remove }) => (
            <div>
              {fields.map(({ key, name, ...restField }) => (
                <div key={key} style={{ display: 'flex', alignItems: 'center', marginBottom: 8, gap: 8 }}>
                  <Form.Item {...restField} name={[name]} noStyle>
                    <Input placeholder="例如: 阿里云盘" style={{ width: 200 }} />
                  </Form.Item>
                  <MinusCircleOutlined
                    onClick={() => remove(name)}
                    style={{ color: '#ff4d4f', cursor: 'pointer', fontSize: 16 }}
                  />
                </div>
              ))}
              <Form.Item noStyle>
                <a href="#!" onClick={(e) => { e.preventDefault(); add(); }} style={{ whiteSpace: 'nowrap' }}>
                  <PlusOutlined /> 添加额外存储
                </a>
              </Form.Item>
            </div>
          )}
        </Form.List>
      </ConfigField>
      <ConfigField
        label="上传路径模板"
        description='模板仅定义目录结构，原始文件名自动保留。支持变量: {{ .Platform }}, {{ .HostName }}, {{ .RoomName }}, {{ now | date "2006-01-02" }}。如需自定义文件名，可加入 {{ .FileName }}'
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'upload_path_tmpl']} noStyle>
          <TextArea
            rows={2}
            placeholder='/录播归档/{{ .Platform }}/{{ .HostName }}/{{ now | date "2006-01-02" }}'
            style={{ width: 500 }}
          />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传后删除本地文件"
        description="上传成功后自动删除本地文件以节省空间"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'delete_after_upload']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传速度限制"
        description="限制单个文件上传速度，防止占用过多带宽影响其他任务，单位为 KB/s，0 表示不限速"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'upload_speed_limit']} noStyle>
          <InputNumber min={0} placeholder="例如: 10240 (10MB/s)" style={{ width: 250 }} addonAfter="KB/s" />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="最大并发上传数"
        description="同时上传的文件数量上限，小文件优先上传，正在上传的文件不会被中断。0 或不填表示顺序上传"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'max_concurrent_uploads']} noStyle>
          <InputNumber min={0} max={10} placeholder="例如: 2" style={{ width: 120 }} />
        </Form.Item>
      </ConfigField>
      <div style={{ marginTop: 16, paddingTop: 16, borderTop: '1px solid #f0f0f0' }}>
        <Space>
          <Button
            icon={<ApiOutlined />}
            onClick={handleTestConnection}
            loading={testing}
          >
            测试连接
          </Button>
          {testResult && (
            <Tag color={testResult.success ? 'green' : 'red'}>
              {testResult.success ? '全部通过' : '存在失败项'}
            </Tag>
          )}
        </Space>
        {testResult && (
          <div style={{ marginTop: 12 }}>
            {testResult.steps.map((step, idx) => (
              <div key={idx} style={{ marginBottom: 4, fontSize: 13, display: 'flex', alignItems: 'center', gap: 6 }}>
                {step.success
                  ? <CheckCircleOutlined style={{ color: '#52c41a' }} />
                  : <CloseCircleOutlined style={{ color: '#ff4d4f' }} />
                }
                <span style={{ fontWeight: 500 }}>{step.name}:</span>
                <span style={{ color: step.success ? '#333' : '#ff4d4f' }}>{step.message}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
};

export default CloudUploadSettings;
