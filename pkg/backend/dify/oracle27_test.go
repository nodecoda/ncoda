package dify_test

// oracle27* constants are the Python YAML oracle outputs
// (serializer.py serialize: pyyaml.dump(allow_unicode=True,
// default_flow_style=False, sort_keys=False); captured 2026-08-14 via
// /tmp/oracle27.py from the SAME fixture sources as the Go tests — see
// /tmp/oracle26/*.yml and task-27-report.md). The loop fixture embeds the
// loop-variable id; pre-P3 Python emitted a random uuid4 and the fixture
// was compared only after loopOracle (yaml_test.go) substituted the Go
// deterministic uuid5 id — since P3 both sides emit the same uuid5.
// oracle27s_* are the sorted-key oracles for the extract/tool/agent
// fixtures (see the note above oracle27s_extract).

const oracle27_minimal = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables: []
    - id: llm_2
      type: custom
      data:
        type: llm
        title: LLM
        model:
          provider: custom
          name: gpt4
          mode: chat
        prompt_template:
        - role: user
          text: Hello
        context:
          enabled: false
          variable_selector: []
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - llm_2
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: llm_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: llm
    - id: edge_2
      source: llm_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_start_params = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: llm_2
      type: custom
      data:
        type: llm
        title: LLM
        model:
          provider: custom
          name: gpt4
          mode: chat
        prompt_template:
        - role: user
          text: '{{#start_1.query#}}'
        context:
          enabled: false
          variable_selector: []
        variables:
        - value_selector:
          - start_1
          - query
          value_type: string
          variable: value_1
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - llm_2
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: llm_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: llm
    - id: edge_2
      source: llm_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_rag41 = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: knowledge_2
      type: custom
      data:
        type: knowledge-retrieval
        title: Knowledge Retrieval
        dataset_ids:
        - ds-001
        - ds-002
        query_variable_selector:
          value_selector:
          - start_1
          - query
          value_type: string
          variable: query
        query: ''
        retrieval_mode: single
        single_retrieval_config:
          model:
            completion_params: {}
            mode: chat
            name: ''
            provider: ''
    - id: llm_3
      type: custom
      data:
        type: llm
        title: LLM
        model:
          provider: openai
          name: gpt-4o
          mode: chat
        prompt_template:
        - role: system
          text: Answer using the retrieved context.
        - role: user
          text: 'Context:

            {{#knowledge_2.result#}}


            Question:

            {{#start_1.query#}}'
        context:
          enabled: false
          variable_selector: []
        variables:
        - value_selector:
          - knowledge_2
          - result
          value_type: array[object]
          variable: value_1
        - value_selector:
          - start_1
          - query
          value_type: string
          variable: value_2
    - id: end_4
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - llm_3
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: knowledge_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: knowledge-retrieval
    - id: edge_2
      source: knowledge_2
      sourceHandle: ''
      target: llm_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: knowledge-retrieval
        targetType: llm
    - id: edge_3
      source: llm_3
      sourceHandle: ''
      target: end_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_http42 = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables: []
    - id: http_2
      type: custom
      data:
        type: http-request
        title: HTTP Request
        method: GET
        url: https://example.com/article
        headers: ''
        params: ''
        body:
          type: none
          data: ''
        authorization:
          type: no-auth
        variables: []
    - id: llm_3
      type: custom
      data:
        type: llm
        title: LLM
        model:
          provider: openai
          name: gpt-4o
          mode: chat
        prompt_template:
        - role: user
          text: 'Summarize this content:

            {{#http_2.body#}}'
        context:
          enabled: false
          variable_selector: []
        variables:
        - value_selector:
          - http_2
          - body
          value_type: string
          variable: value_1
    - id: end_4
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - llm_3
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: http_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: http-request
    - id: edge_2
      source: http_2
      sourceHandle: ''
      target: llm_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: http-request
        targetType: llm
    - id: edge_3
      source: llm_3
      sourceHandle: ''
      target: end_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_extract = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: text
          type: text-input
          variable: text
          required: true
    - id: extract_2
      type: custom
      data:
        type: parameter-extractor
        title: Parameter Extractor
        model:
          provider: provider
          name: model
          mode: chat
        parameters:
        - name: city
          type: string
          description: Destination city
          required: true
        - name: days
          type: number
          description: Trip duration in days
          required: true
        - name: flexible
          type: boolean
          description: Whether travel dates are flexible
          required: true
        - name: tags
          type: array[string]
          description: Travel preferences
          required: true
        query:
        - start_1
        - text
        instruction: Extract a travel request
        reasoning_mode: prompt
        vision:
          enabled: false
    - id: extract_result_3
      type: custom
      data:
        type: code
        title: Code
        code_language: python3
        code: "def main(__is_success, __reason, __usage, field_0, field_1, field_2,\
          \ field_3):\n    return {\n        \"ok\": bool(__is_success),\n       \
          \ \"reason\": __reason or \"\",\n        \"usage\": __usage or {},\n   \
          \     \"value\": {\n            \"city\": field_0,\n            \"days\"\
          : field_1,\n            \"flexible\": field_2,\n            \"tags\": field_3,\n\
          \        },\n    }"
        variables:
        - value_selector:
          - extract_2
          - __is_success
          value_type: number
          variable: __is_success
        - value_selector:
          - extract_2
          - __reason
          value_type: string
          variable: __reason
        - value_selector:
          - extract_2
          - __usage
          value_type: object
          variable: __usage
        - value_selector:
          - extract_2
          - city
          value_type: string
          variable: field_0
        - value_selector:
          - extract_2
          - days
          value_type: number
          variable: field_1
        - value_selector:
          - extract_2
          - flexible
          value_type: boolean
          variable: field_2
        - value_selector:
          - extract_2
          - tags
          value_type: array[string]
          variable: field_3
        outputs:
          value:
            type: object
            children:
              city:
                type: string
                children: null
              days:
                type: number
                children: null
              flexible:
                type: boolean
                children: null
              tags:
                type: array[string]
                children: null
          ok:
            type: boolean
            children: null
          reason:
            type: string
            children: null
          usage:
            type: object
            children: null
    - id: output_4
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: city
          value_type: string
          value_selector:
          - extract_result_3
          - value
          - city
    - id: output_5
      type: custom
      data:
        type: end
        title: Output 2
        outputs:
        - variable: ok
          value_type: boolean
          value_selector:
          - extract_result_3
          - ok
    - id: output_6
      type: custom
      data:
        type: end
        title: Output 3
        outputs:
        - variable: reason
          value_type: string
          value_selector:
          - extract_result_3
          - reason
    - id: output_7
      type: custom
      data:
        type: end
        title: Output 4
        outputs:
        - variable: usage
          value_type: object
          value_selector:
          - extract_result_3
          - usage
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: extract_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: parameter-extractor
    - id: edge_2
      source: extract_2
      sourceHandle: source
      target: extract_result_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: parameter-extractor
        targetType: code
    - id: edge_3
      source: extract_result_3
      sourceHandle: ''
      target: output_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: end
    - id: edge_4
      source: output_4
      sourceHandle: ''
      target: output_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: end
        targetType: end
    - id: edge_5
      source: output_5
      sourceHandle: ''
      target: output_6
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: end
        targetType: end
    - id: edge_6
      source: output_6
      sourceHandle: ''
      target: output_7
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: end
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_classify = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: classify_2
      type: custom
      data:
        type: question-classifier
        title: Question Classifier
        query_variable_selector:
        - start_1
        - query
        model:
          provider: provider
          name: model
          mode: chat
        classes:
        - id: support
          name: Technical support request
          label: ''
        - id: unknown
          name: Cannot determine intent
          label: ''
        - id: billing
          name: Billing request
          label: ''
        instruction: Classify the request
        vision:
          enabled: false
    - id: classify_support_3
      type: custom
      data:
        type: code
        title: Code
        code_language: python3
        code: "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"support\"\
          }"
        variables: []
        outputs:
          result:
            type: string
            children: null
    - id: classify_unknown_4
      type: custom
      data:
        type: code
        title: Code 2
        code_language: python3
        code: "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"unknown\"\
          }"
        variables: []
        outputs:
          result:
            type: string
            children: null
    - id: classify_billing_5
      type: custom
      data:
        type: code
        title: Code 3
        code_language: python3
        code: "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"billing\"\
          }"
        variables: []
        outputs:
          result:
            type: string
            children: null
    - id: classify_result_6
      type: custom
      data:
        type: variable-aggregator
        title: Variable Aggregator
        variables:
        - - classify_support_3
          - result
        - - classify_unknown_4
          - result
        - - classify_billing_5
          - result
        output_type: string
    - id: end_7
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - classify_result_6
          - output
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: classify_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: question-classifier
    - id: edge_2
      source: classify_2
      sourceHandle: support
      target: classify_support_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: question-classifier
        targetType: code
    - id: edge_3
      source: classify_2
      sourceHandle: unknown
      target: classify_unknown_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: question-classifier
        targetType: code
    - id: edge_4
      source: classify_2
      sourceHandle: billing
      target: classify_billing_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: question-classifier
        targetType: code
    - id: edge_5
      source: classify_support_3
      sourceHandle: ''
      target: classify_result_6
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: variable-aggregator
    - id: edge_6
      source: classify_unknown_4
      sourceHandle: ''
      target: classify_result_6
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: variable-aggregator
    - id: edge_7
      source: classify_billing_5
      sourceHandle: ''
      target: classify_result_6
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: variable-aggregator
    - id: edge_8
      source: classify_result_6
      sourceHandle: ''
      target: end_7
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: variable-aggregator
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_agent = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: agent_2
      type: custom
      data:
        type: agent
        title: agent
        output_schema: null
        agent_strategy_name: function_calling
        agent_strategy_provider_name: langgenius/agent
        agent_strategy_label: Function Calling
        agent_parameters:
          model:
            value:
              completion_params: {}
              mode: chat
              model: model
              model_type: llm
              provider: provider
              type: model-selector
            type: constant
          query:
            value: '{{#start_1.query#}}'
            type: constant
          instruction:
            value: Answer the user
            type: constant
          tools:
            value:
            - enabled: true
              extra: {}
              parameters: {}
              plugin_unique_identifier: langgenius/time:1.0.0@sha256:time
              provider_name: time
              tool_name: current_time
              type: builtin
            type: constant
        plugin_unique_identifier: langgenius/agent:1.0.0@sha256:agent
        tool_node_version: '2'
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - agent_2
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: agent_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: agent
    - id: edge_2
      source: agent_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: agent
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_loop = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables: []
    - id: loop_start_3
      type: custom-loop-start
      data:
        type: loop-start
        selected: false
        title: Loop Start
        desc: ''
        loop_id: loop_2
        isInLoop: true
      parentId: loop_2
      zIndex: 1002
    - id: loop_2
      type: custom
      data:
        type: loop
        title: Loop
        loop_variables:
        - label: num
          value_type: constant
          var_type: number
          id: 40977302-1ce1-4a74-a07f-d09acd5811db
          value: 1
        break_conditions:
        - variable_selector:
          - loop_2
          - num
          comparison_operator: ≥
          value: '5'
        loop_count: 10
        logical_operator: and
        start_node_id: loop_start_3
    - id: assigner_4
      type: custom
      data:
        type: assigner
        title: assigner
        loop_id: loop_2
        isInLoop: true
        items:
        - variable_selector:
          - loop_2
          - num
          operation: +=
          input_type: constant
          value: 1
        version: '2'
      parentId: loop_2
      zIndex: 1002
    - id: end_5
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: number
          value_selector:
          - loop_2
          - num
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: loop_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: loop
    - id: edge_2
      source: loop_start_3
      sourceHandle: source
      target: assigner_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: true
        sourceType: loop-start
        targetType: assigner
        loop_id: loop_2
      zIndex: 1002
    - id: edge_3
      source: loop_2
      sourceHandle: ''
      target: end_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: loop
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_for_expr = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: item
          type: text-input
          variable: item
          required: true
    - id: code_2
      type: custom
      data:
        type: code
        title: Code
        code_language: python3
        code: "# ncoda:shim 1\ndef main(item) -> dict:\n    return {\"result\": [item]}"
        variables:
        - value_selector:
          - start_1
          - item
          value_type: string
          variable: item
        outputs:
          result:
            type: array[string]
            children: null
    - id: iteration_start_4
      type: custom-iteration-start
      data:
        type: iteration-start
        selected: false
        title: Iteration Start
        desc: ''
        isInIteration: true
        iteration_id: iteration_3
      parentId: iteration_3
      zIndex: 1002
    - id: iteration_3
      type: custom
      data:
        type: iteration
        title: Iteration
        iterator_selector:
        - code_2
        - result
        output_selector:
        - iteration_3
        - item
        output_type: array[string]
        iterator_input_type: array[string]
        start_node_id: iteration_start_4
        flatten_output: false
    - id: end_5
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: array[string]
          value_selector:
          - iteration_3
          - output
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: code_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: code
    - id: edge_2
      source: code_2
      sourceHandle: ''
      target: iteration_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: iteration
    - id: edge_3
      source: iteration_3
      sourceHandle: ''
      target: end_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: iteration
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`
const oracle27_tool = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: tool_2
      type: custom
      data:
        type: tool
        title: Tool
        tool_name: google_search
        provider_id: google_search
        tool_parameters:
          query: '{{#start_1.query#}}'
          max_results: 5
        tool_configuration: {}
        tool_configurations: {}
        output_schema: {}
        paramSchemas: []
        params: {}
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - tool_2
          - result
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: tool_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: tool
    - id: edge_2
      source: tool_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: tool
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_extract_text = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: doc
          type: file
          variable: doc
          required: true
          allowed_file_types:
          - document
          allowed_file_extensions:
          - .pdf
          allowed_file_upload_methods: []
    - id: extract_text_2
      type: custom
      data:
        type: document-extractor
        title: Document Extractor
        variable_selector:
        - start_1
        - doc
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - extract_text_2
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: extract_text_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: document-extractor
    - id: edge_2
      source: extract_text_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: document-extractor
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_ifelse = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
        - label: language
          type: text-input
          variable: language
          default: Chinese
    - id: if_2
      type: custom
      data:
        type: if-else
        title: IF/ELSE
        cases:
        - case_id: if_2_case_0
          logical_operator: and
          conditions:
          - variable_selector:
            - start_1
            - language
            comparison_operator: is
            value: Chinese
        - case_id: if_2_case_1
          logical_operator: and
          conditions:
          - variable_selector:
            - start_1
            - language
            comparison_operator: is
            value: Chinese
    - id: llm_3
      type: custom
      data:
        type: llm
        title: LLM
        model:
          provider: openai_api_compatible
          name: gpt-5.4
          mode: chat
          completion_params:
            temperature: 0.5
            max_tokens: 2048
        prompt_template:
        - role: user
          text: Chinese
        context:
          enabled: false
          variable_selector: []
    - id: llm_4
      type: custom
      data:
        type: llm
        title: LLM 2
        model:
          provider: openai_api_compatible
          name: gpt-5.4
          mode: chat
          completion_params:
            temperature: 0.5
            max_tokens: 1024
        prompt_template:
        - role: user
          text: English
        context:
          enabled: false
          variable_selector: []
    - id: end_5
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - llm_3
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: if_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: if-else
    - id: edge_2
      source: if_2
      sourceHandle: if_2_case_0
      target: llm_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: if-else
        targetType: llm
    - id: edge_3
      source: if_2
      sourceHandle: if_2_case_1
      target: llm_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: if-else
        targetType: llm
    - id: edge_4
      source: if_2
      sourceHandle: 'false'
      target: llm_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: if-else
        targetType: llm
    - id: edge_5
      source: llm_3
      sourceHandle: ''
      target: end_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    - id: edge_6
      source: llm_4
      sourceHandle: ''
      target: end_5
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: llm
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_answer = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: answer_2
      type: custom
      data:
        type: answer
        title: Answer
        answer: hi
        variables: []
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_human_input = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: n_1
      type: custom
      data:
        type: human-input
        title: Human Input
        form_content: Please fill in
        inputs:
        - type: paragraph
          output_variable_name: note
          default:
            type: constant
            selector:
            - n_1
            - x
            value: hi
        - type: select
          output_variable_name: pick
          option_source:
            type: constant
            selector:
            - n_1
            - x
            value:
            - a
            - b
        - type: file
          output_variable_name: doc
          allowed_file_types:
          - document
          allowed_file_extensions:
          - .pdf
          allowed_file_upload_methods:
          - local_file
        - type: file-list
          output_variable_name: docs
          allowed_file_types:
          - document
          allowed_file_extensions:
          - .pdf
          allowed_file_upload_methods:
          - local_file
          number_limits: 5
        user_actions:
        - id: ua_1
          title: Confirm
          button_style: primary
        timeout: 36
        timeout_unit: hour
        delivery_methods:
        - id: dm_1
          type: webapp
          enabled: true
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_datasource = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: n_1
      type: custom
      data:
        type: datasource
        title: Datasource
        plugin_id: langgenius/notion
        provider_name: notion
        provider_type: plugin
        datasource_name: local_file
        datasource_configurations:
          page_size: 10
        plugin_unique_identifier: langgenius/notion:0.0.1
        datasource_parameters:
          db:
            value: pages
            type: string
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_list_operator = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: n_1
      type: custom
      data:
        type: list-operator
        title: List Operator
        variable:
        - n_1
        - items
        filter_by:
          enabled: true
          conditions:
          - key: price
            comparison_operator: '>'
            value: 10
        extract_by:
          enabled: true
          serial: '1'
        order_by:
          enabled: true
          key: name
          value: asc
        limit:
          enabled: true
          size: 5
        var_type: array[string]
        item_var_type: string
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_custom_note = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: n_1
      type: custom-note
      data:
        type: ''
        selected: false
        text: Remember
        author: alice
        showAuthor: true
        theme: blue
        height: 200
        width: 300
      zIndex: 1001
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_edges_special = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: outer_1
      type: custom-simple
      data:
        type: loop-end
        title: Loop End
        loop_id: loop_1
        isInLoop: true
    - id: inner_1
      type: custom-simple
      data:
        type: loop-end
        title: Loop End
        loop_id: loop_1
        isInLoop: true
    edges:
    - id: e_1
      source: n_1
      sourceHandle: source
      target: n_2
      targetHandle: target
      type: custom
      data:
        isInIteration: true
        isInLoop: true
        sourceType: ''
        targetType: ''
        loop_id: loop_1
      zIndex: 1002
    - id: e_2
      source: outer_1
      sourceHandle: source
      target: inner_1
      targetHandle: target
      type: custom
      data:
        isInIteration: true
        isInLoop: true
        sourceType: loop-end
        targetType: loop-end
        iteration_id: iter_9
        loop_id: loop_1
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_var_agg = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: agg_1
      type: custom
      data:
        type: variable-aggregator
        title: Aggregate
        variables:
        - - a_1
          - x
        - - b_1
          - y
        output_type: array[string]
        advanced_settings:
          group_enabled: true
    edges: []
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27_viewport_vars = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables:
  - id: cv_1
    name: topic
    value_type: integer
    value: 42
    description: desc
    selector:
    - conversation
    - topic
  environment_variables:
  - id: ev_1
    name: KEY
    value_type: string
    value: val
    description: env desc
    selector:
    - environment
    - KEY
  rag_pipeline_variables:
  - '{{#start_1.x#}}'
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes: []
    edges: []
    viewport:
      x: 2
      y: -1.5
      zoom: 0.75
dependencies: []
`

// oracle27s* constants are the SORTED-KEY oracles for the three fixtures
// whose map-backed payloads Go emits with sorted keys (Task 26 normalizeAny
// deviation): extract (code-node outputs), tool (tool_parameters),
// agent (agent_parameters). tool/agent (oracle27s_tool / oracle27s_agent)
// are byte-identical to genuine pyyaml outputs of the sorted-key dicts
// (/tmp/oracle27-sorted/*.yml, generated 2026-08-14 via
// /tmp/oracle27_sorted.py) and are true byte-parity oracles.
//
// oracle27s_extract is NOT a single pyyaml dump: Go's normalizeAny sorts at
// TWO levels — the top-level "outputs" keys AND the nested `children` field
// dicts (so "children" precedes "type" there). The level-1 field dicts
// (ok/reason/usage/value) keep their insertion order ("type" first). The
// result is a hybrid key order no pyyaml output produces.
// The row is therefore SEMANTIC-ONLY: compared byte-wise as a determinism /
// regression lock, with value equality against the true Python oracle
// (/tmp/oracle26/extract.yml) verified via pyyaml.safe_load deep-compare
// (see task-27-report.md R1).

const oracle27s_extract = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: text
          type: text-input
          variable: text
          required: true
    - id: extract_2
      type: custom
      data:
        type: parameter-extractor
        title: Parameter Extractor
        model:
          provider: provider
          name: model
          mode: chat
        parameters:
        - name: city
          type: string
          description: Destination city
          required: true
        - name: days
          type: number
          description: Trip duration in days
          required: true
        - name: flexible
          type: boolean
          description: Whether travel dates are flexible
          required: true
        - name: tags
          type: array[string]
          description: Travel preferences
          required: true
        query:
        - start_1
        - text
        instruction: Extract a travel request
        reasoning_mode: prompt
        vision:
          enabled: false
    - id: extract_result_3
      type: custom
      data:
        type: code
        title: Code
        code_language: python3
        code: "def main(__is_success, __reason, __usage, field_0, field_1, field_2,\
          \ field_3):\n    return {\n        \"ok\": bool(__is_success),\n       \
          \ \"reason\": __reason or \"\",\n        \"usage\": __usage or {},\n   \
          \     \"value\": {\n            \"city\": field_0,\n            \"days\"\
          : field_1,\n            \"flexible\": field_2,\n            \"tags\": field_3,\n\
          \        },\n    }"
        variables:
        - value_selector:
          - extract_2
          - __is_success
          value_type: number
          variable: __is_success
        - value_selector:
          - extract_2
          - __reason
          value_type: string
          variable: __reason
        - value_selector:
          - extract_2
          - __usage
          value_type: object
          variable: __usage
        - value_selector:
          - extract_2
          - city
          value_type: string
          variable: field_0
        - value_selector:
          - extract_2
          - days
          value_type: number
          variable: field_1
        - value_selector:
          - extract_2
          - flexible
          value_type: boolean
          variable: field_2
        - value_selector:
          - extract_2
          - tags
          value_type: array[string]
          variable: field_3
        outputs:
          value:
            type: object
            children:
              city:
                type: string
                children: null
              days:
                type: number
                children: null
              flexible:
                type: boolean
                children: null
              tags:
                type: array[string]
                children: null
          ok:
            type: boolean
            children: null
          reason:
            type: string
            children: null
          usage:
            type: object
            children: null
    - id: end_4
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: object
          value_selector:
          - extract_result_3
          - value
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: extract_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: parameter-extractor
    - id: edge_2
      source: extract_2
      sourceHandle: source
      target: extract_result_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: parameter-extractor
        targetType: code
    - id: edge_3
      source: extract_result_3
      sourceHandle: ''
      target: end_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: code
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27s_tool = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: tool_2
      type: custom
      data:
        type: tool
        title: Tool
        tool_name: google_search
        provider_id: google_search
        tool_parameters:
          query: '{{#start_1.query#}}'
          max_results: 5
        tool_configuration: {}
        tool_configurations: {}
        output_schema: {}
        paramSchemas: []
        params: {}
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - tool_2
          - result
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: tool_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: tool
    - id: edge_2
      source: tool_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: tool
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

const oracle27s_agent = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: query
          type: text-input
          variable: query
          required: true
    - id: agent_2
      type: custom
      data:
        type: agent
        title: agent
        output_schema: null
        agent_strategy_name: function_calling
        agent_strategy_provider_name: langgenius/agent
        agent_strategy_label: Function Calling
        agent_parameters:
          instruction:
            value: Answer the user
            type: constant
          model:
            value:
              completion_params: {}
              mode: chat
              model: model
              model_type: llm
              provider: provider
              type: model-selector
            type: constant
          query:
            value: '{{#start_1.query#}}'
            type: constant
          tools:
            value:
            - enabled: true
              extra: {}
              parameters: {}
              plugin_unique_identifier: langgenius/time:1.0.0@sha256:time
              provider_name: time
              tool_name: current_time
              type: builtin
            type: constant
        plugin_unique_identifier: langgenius/agent:1.0.0@sha256:agent
        tool_node_version: '2'
    - id: end_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - agent_2
          - text
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: agent_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: agent
    - id: edge_2
      source: agent_2
      sourceHandle: ''
      target: end_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: agent
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`

// oracle27_tpl_var: template assigned to a variable (both Python and Go
// emit a template-transform node — byte parity, unlike the return-inline
// templateSrc fixture; see task-27-report.md).
const oracle27_tpl_var = `app:
  mode: workflow
  name: ''
kind: app
version: 0.6.0
workflow:
  conversation_variables: []
  environment_variables: []
  rag_pipeline_variables: []
  features:
    file_upload: {}
    sensitive_word_avoidance:
      enabled: false
    text_to_speech:
      enabled: false
      language: ''
      voice: ''
    opening_statement: ''
    retriever_resource:
      enabled: false
    speech_to_text:
      enabled: false
    suggested_questions: []
    suggested_questions_after_answer:
      enabled: false
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        title: User Input
        variables:
        - label: name
          type: text-input
          variable: name
          required: true
        - label: score
          type: number
          variable: score
          required: true
    - id: template_2
      type: custom
      data:
        type: template-transform
        title: Template
        template: Hello {{#start_1.name#}}, score {{#start_1.score#}}
        variables: []
    - id: output_3
      type: custom
      data:
        type: end
        title: Output
        outputs:
        - variable: message
          value_type: string
          value_selector:
          - template_2
          - output
    - id: end_4
      type: custom
      data:
        type: end
        title: Output 2
        outputs:
        - variable: output
          value_type: string
          value_selector:
          - template_2
          - output
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: template_2
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: start
        targetType: template-transform
    - id: edge_2
      source: template_2
      sourceHandle: ''
      target: output_3
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: template-transform
        targetType: end
    - id: edge_3
      source: output_3
      sourceHandle: ''
      target: end_4
      targetHandle: target
      type: custom
      data:
        isInIteration: false
        isInLoop: false
        sourceType: end
        targetType: end
    viewport:
      x: 0
      y: 0
      zoom: 1
dependencies: []
`
