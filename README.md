# 机器人远程控制协议实现文档

## 项目概述

使用 QUIC 协议通信，采用 MessagePack 进行数据序列化。支持传输传感器数据、图像流以及动作控制指令，

## 消息协议
基于需求设计，满足：
1. header 结构、各个字段可选值
2. body 采用 messagepack 序列化
3. 小端序写入

## 启动流程
1. 支持 Server 启动时初始化 Config(目前包含 sc 和 cs）, 以便后续发送给 Client
2. Client 与 Server 建立连接时发送 Get 请求, 获取 Config, 再进行数据传输
3. 发送报文时机由开发者自定义, 主要是根据 sc 和 cs, 计算 1/sc 和 1/cs 手动控制发送流程, attach 暂未实现